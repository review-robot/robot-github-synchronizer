package sync

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/v36/github"
	"github.com/opensourceways/community-robot-lib/utils"
	"github.com/sirupsen/logrus"

	"github.com/opensourceways/robot-github-synchronizer/config"
)

const (
	syncIssueEndPoint    = "/synchronization/gitee/issue"
	syncCommentEndPoint  = "/synchronization/gitee/comment"
	syncedIssueMsg       = `**SYNCED PROMPT:**  This issue has been synchronized with [another issue](%s). <!--- %s -->`
	syncedIssueNotice    = `> Note: this issue is create by %s at %s . You can still comment on this issue and the author will be notified.`
	syncedCommentContent = `> %s create at %s

%s
`
)

var (
	syncedInfoReg           = regexp.MustCompile(`<!--- (.*) -->`)
	syncIssueMsgReg         = regexp.MustCompile(fmt.Sprintf(`\*\*SYNCED PROMPT:\*\*  This issue has been synchronized with \[another issue\]\(%s\). <!--- %s -->`, "(.*)", "(.*)"))
	syncedCommentContentReg = regexp.MustCompile(fmt.Sprintf(`> %s create at %s\n\n%s`, "(.*)", "(.*)", "(.*)"))
	syncedIssueContentReg   = regexp.MustCompile(fmt.Sprintf(syncedIssueNotice, "(.*)", "(.*)"))
	checkIssueIDReg         = regexp.MustCompile(`#[0-9a-zA-Z]+\b`)
)

// Synchronize the sync calling the sync service
type Synchronize struct {
	utils.HttpClient
	gc           *github.Client
	Endpoint     *url.URL
	synchronizer string
}

// HandleSyncIssueToGitHub synchronize the Issue of the gitee platform to the Github platform
func (sc *Synchronize) HandleSyncIssueToGitee(org, repo string, e *github.Issue, cfg *config.BotConfig) error {
	if !sc.needSyncIssue(cfg, e) {
		logrus.Infof("Issue %s does't need to be synchronized", e.GetHTMLURL())

		return nil
	}

	content := sc.processIssueIdInContent(org, repo, e.GetBody())
	om := cfg.OrgMapping(org)
	issue := reqIssue{
		orgRepo: orgRepo{Org: om, Repo: repo},
		Title:   e.GetTitle(),
		Content: combinedIssueContent(content, e),
	}

	v, err := sc.createGiteeIssue(issue)
	if err != nil {
		return err
	}

	return sc.addIssueSyncedMsg(org, repo, v, e)
}

// HandleSyncIssueComment synchronize the comments of the gitee platform Issue to the Github platform
func (sc *Synchronize) HandleSyncIssueComment(org, repo string, e *github.IssueCommentEvent, cfg *config.BotConfig) error {
	if !sc.needSyncIssueComment(cfg, e.GetComment()) {
		logrus.Infof("Comment %s does't need to be synchronized", e.GetComment().GetHTMLURL())

		return nil
	}

	info, err := sc.findSyncedIssueInfoFromComments(org, repo, e.GetIssue().GetNumber())
	if err != nil {
		return err
	}

	content := sc.processIssueIdInContent(org, repo, e.GetComment().GetBody())
	req := reqComment{
		orgRepo: orgRepo{Org: info.GiteeOrg, Repo: info.GiteeRepo},
		Number:  info.GiteeIssueNumber,
		Content: combinedIssueCommentContent(content, e.GetComment()),
	}

	return sc.createGiteeComment(req)
}

func (sc *Synchronize) addIssueSyncedMsg(org, repo string, si *issueSyncedInfo, issue *github.Issue) error {
	var mErr utils.MultiError
	isr := issueSyncedRelation{
		GiteeOrg:         si.Org,
		GiteeRepo:        si.Repo,
		GiteeIssueNumber: si.Number,
		GithubOrg:        org,
		GithubRepo:       repo,
		GithubNumber:     strconv.Itoa(issue.GetNumber()),
	}

	ds, err := encodeObject(&isr)
	if err != nil {
		return err
	}

	teeComment := fmt.Sprintf(syncedIssueMsg, issue.GetHTMLURL(), ds)
	mErr.AddError(sc.createGiteeComment(reqComment{
		orgRepo: orgRepo{si.Org, si.Repo},
		Number:  si.Number,
		Content: teeComment,
	}))

	isr.IsOrigin = true
	ds, err = encodeObject(&isr)
	if err != nil {
		return err
	}

	hubComment := fmt.Sprintf(syncedIssueMsg, si.Link, ds)
	_, _, err = sc.gc.Issues.CreateComment(
		context.Background(), org, repo, issue.GetNumber(),
		&github.IssueComment{Body: &hubComment},
	)
	mErr.AddError(err)

	return mErr.Err()
}

func (sc *Synchronize) createGiteeComment(comment reqComment) error {
	payload, err := utils.JsonMarshal(&comment)
	if err != nil {
		return err
	}
	uri := sc.getCallURL(syncCommentEndPoint)

	req, err := http.NewRequest(http.MethodPost, uri, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	return sc.forwardTo(req, nil)
}

func (sc *Synchronize) createGiteeIssue(issue reqIssue) (*issueSyncedInfo, error) {
	payload, err := utils.JsonMarshal(&issue)
	if err != nil {
		return nil, err
	}

	uri := sc.getCallURL(syncIssueEndPoint)

	req, err := http.NewRequest(http.MethodPost, uri, bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}

	var resp issueSyncedResp
	if err := sc.forwardTo(req, &resp); err != nil {
		return nil, err
	}

	return &resp.Data, nil
}

func (sc *Synchronize) forwardTo(req *http.Request, jrp interface{}) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gitte-synchronizer")

	return sc.ForwardTo(req, jrp)
}

func (sc *Synchronize) getCallURL(p string) string {
	v := *sc.Endpoint
	v.Path = path.Join(v.Path, p)

	return v.String()
}

func (sc *Synchronize) findSyncedIssueInfoFromComments(org, repo string, number int) (*issueSyncedRelation, error) {
	comments, _, err := sc.gc.Issues.ListComments(context.Background(), org, repo, number, nil)
	if err != nil {
		return nil, err
	}

	for _, v := range comments {
		if si, b := parseSyncedIssueInfo(v); b {
			return si, nil
		}
	}

	return nil, fmt.Errorf("PR %s/%s/%d is not synced", org, repo, number)
}

func (sc *Synchronize) needSyncIssue(cfg *config.BotConfig, issue *github.Issue) bool {
	if !cfg.EnableSyncIssue {
		return false
	}

	author := issue.GetUser().GetLogin()
	if sc.isCreateSyncIssue(issue.GetBody(), author) {
		return false
	}

	nsc := sc.getDoNotSyncByAuthor(cfg, author)
	if nsc == nil {
		return true
	}

	return nsc.NeedSyncIssue
}

func (sc *Synchronize) isCreateSyncIssue(body, author string) bool {
	if strings.ToLower(author) != sc.synchronizer {
		return false
	}

	return syncedIssueContentReg.MatchString(body)
}

func (sc *Synchronize) needSyncIssueComment(cfg *config.BotConfig, comment *github.IssueComment) bool {
	if !cfg.EnableSyncComment {
		return false
	}

	author := comment.GetUser().GetLogin()
	body := comment.GetBody()

	if sc.isCreateSyncIssueComment(body, author) {
		return false
	}

	nsc := sc.getDoNotSyncByAuthor(cfg, author)
	if nsc == nil {
		return true
	}

	return nsc.CommentContentInWhitelist(body)
}

func (sc *Synchronize) isCreateSyncIssueComment(body, author string) bool {
	if strings.ToLower(author) != sc.synchronizer {
		return false
	}

	return syncedCommentContentReg.MatchString(body)
}

func (sc *Synchronize) getDoNotSyncByAuthor(cfg *config.BotConfig, author string) (nsc *config.NotSyncConfig) {
	for i := range cfg.DoNotSyncAuthors {
		tmp := cfg.DoNotSyncAuthors[i]
		if strings.ToLower(author) == strings.ToLower(tmp.Account) {
			nsc = &tmp

			return
		}
	}

	if strings.ToLower(author) == sc.synchronizer {
		nsc = &config.NotSyncConfig{Account: sc.synchronizer}
	}

	return
}

func (sc *Synchronize) HandleSyncIssueStatus(org, repo string, issue *github.Issue, cfg *config.BotConfig) error {
	if !cfg.EnableSyncIssue {
		return nil
	}

	info, err := sc.findSyncedIssueInfoFromComments(org, repo, issue.GetNumber())
	if err != nil {
		return err
	}

	if !info.IsOrigin {
		logrus.Infof("issue %s is not sync origin no need sync status", issue.GetHTMLURL())

		return nil
	}
	p := reqUpdateIssueState{
		orgRepo: orgRepo{info.GiteeOrg, info.GiteeRepo},
		Number:  info.GiteeIssueNumber,
		State:   issue.GetState(),
	}

	payload, err := utils.JsonMarshal(&p)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPut, sc.getCallURL(syncIssueEndPoint), bytes.NewBuffer(payload))
	if err != nil {
		return err
	}

	return sc.forwardTo(req, nil)
}

func (sc *Synchronize) processIssueIdInContent(org, repo, content string) string {
	matches := checkIssueIDReg.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return content
	}

	for _, v := range matches {
		if len(v) > 0 {
			iid := v[0]
			tiid := sc.transformIssueID(org, repo, iid)

			content = strings.Replace(content, iid, tiid, 1)
		}
	}

	return content
}

func (sc *Synchronize) transformIssueID(org string, repo string, iid string) string {
	iid = strings.Trim(iid, "#")

	number, err := strconv.Atoi(iid)
	if err != nil {
		return fmt.Sprintf("#<!--- -->%s", iid)
	}

	issue, _, err := sc.gc.Issues.Get(context.Background(), org, repo, number)
	if err != nil || issue == nil {
		return fmt.Sprintf("#<!--- -->%s", iid)
	}

	if issue.GetPullRequestLinks() != nil {
		return fmt.Sprintf("[#<!--- -->%s](%s)", iid, issue.GetHTMLURL())
	}

	comment, err := sc.findSyncedIssueInfoFromComments(org, repo, issue.GetNumber())
	if err != nil || comment == nil {
		return fmt.Sprintf("[#<!--- -->%s](%s)", iid, issue.GetHTMLURL())
	}

	return fmt.Sprintf("#%s", comment.GiteeIssueNumber)
}

func combinedIssueContent(content string, e *github.Issue) string {
	contentTpl := `%s

%s
`
	author := fmt.Sprintf("[%s](%s)", e.GetUser().GetName(), e.GetUser().GetHTMLURL())
	platform := fmt.Sprintf("[gihub](%s)", e.GetHTMLURL())

	notice := fmt.Sprintf(syncedIssueNotice, author, platform)

	return fmt.Sprintf(contentTpl, notice, content)
}

func combinedIssueCommentContent(content string, e *github.IssueComment) string {
	author := fmt.Sprintf("[%s](%s)", e.GetUser().GetLogin(), e.GetUser().GetHTMLURL())
	platform := fmt.Sprintf("[github](%s)", e.GetHTMLURL())

	return fmt.Sprintf(syncedCommentContent, author, platform, content)
}

func parseSyncedIssueInfo(comment *github.IssueComment) (*issueSyncedRelation, bool) {
	body := comment.GetBody()
	if !syncIssueMsgReg.MatchString(body) {
		return nil, false
	}

	matches := syncedInfoReg.FindAllStringSubmatch(body, -1)
	if len(matches) != 1 || len(matches[0]) != 2 {
		return nil, false
	}

	infoStr := matches[0][1]
	info := new(issueSyncedRelation)

	if err := decodeObject(infoStr, info); err != nil {
		logrus.WithError(err).Error("parse synced issue info fail")

		return nil, false
	}

	return info, true
}

func encodeObject(data interface{}) (string, error) {
	marshal, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(marshal), nil
}

func decodeObject(data string, obj interface{}) error {
	ds, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return err
	}

	return json.Unmarshal(ds, obj)
}

func NewSynchronize(syncSrvAddr string, gc *github.Client, synchronizer string) (Synchronize, error) {
	uri, err := url.Parse(syncSrvAddr)
	if err != nil {
		return Synchronize{}, err
	}

	return Synchronize{
		gc:           gc,
		Endpoint:     uri,
		HttpClient:   utils.HttpClient{MaxRetries: 3},
		synchronizer: synchronizer,
	}, nil
}
