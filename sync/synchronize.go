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
	"strconv"

	"github.com/google/go-github/v36/github"
	"github.com/opensourceways/community-robot-lib/utils"
)

const (
	syncIssueEndPoint   = "/synchronization/gitee/issue"
	syncCommentEndPoint = "/synchronization/gitee/comment"
	syncedIssueMsg      = `**SYNCED PROMPT:**  current issue has been synced with [it](%s) <!--- %s -->`
)

// Synchronizer the sync calling the sync service
type Synchronizer struct {
	utils.HttpClient

	gc *github.Client

	// Endpoint the root path of the request
	Endpoint *url.URL
}

// HandleSyncIssueToGitHub synchronize the Issue of the gitee platform to the Github platform
func (sc *Synchronizer) HandleSyncIssueToGitee(org, repo string, e *github.Issue) error {
	issue := reqIssue{
		orgRepo: orgRepo{Org: "cve-manage-test", Repo: "config"},
		Title:   e.GetTitle(),
		Content: e.GetBody(),
	}

	v, err := sc.createGiteeIssue(issue)
	if err != nil {
		return err
	}

	return sc.addIssueSyncedMsg(org, repo, v, e)
}

// HandleSyncIssueComment synchronize the comments of the gitee platform Issue to the Github platform
func (sc *Synchronizer) HandleSyncIssueComment(org, repo string, e *github.IssueComment) error {
	return nil
}

func (sc *Synchronizer) addIssueSyncedMsg(org, repo string, si *issueSyncedInfo, issue *github.Issue) error {
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

	var mErr utils.MultiError

	hubComment := fmt.Sprintf(syncedIssueMsg, si.Link, ds)
	teeComment := fmt.Sprintf(syncedIssueMsg, issue.GetHTMLURL(), ds)

	// add a sync msg to gitee issue
	mErr.AddError(sc.createGiteeComment(reqComment{
		orgRepo: orgRepo{si.Org, si.Repo},
		Number:  si.Number,
		Content: teeComment,
	}))

	// add sync issue success msg to github
	_, _, err = sc.gc.Issues.CreateComment(
		context.Background(), org, repo, issue.GetNumber(),
		&github.IssueComment{Body: &hubComment},
	)
	mErr.AddError(err)

	return mErr.Err()
}

func (sc *Synchronizer) createGiteeComment(comment reqComment) error {
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

func (sc *Synchronizer) createGiteeIssue(issue reqIssue) (*issueSyncedInfo, error) {
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

func (sc *Synchronizer) forwardTo(req *http.Request, jrp interface{}) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gitte-synchronizer")

	return sc.ForwardTo(req, jrp)
}

func (sc *Synchronizer) getCallURL(p string) string {
	v := *sc.Endpoint
	v.Path = path.Join(v.Path, p)

	return v.String()
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

func NewSynchronize(syncSrvAddr string, gc *github.Client) (Synchronizer, error) {
	uri, err := url.Parse(syncSrvAddr)
	if err != nil {
		return Synchronizer{}, err
	}

	return Synchronizer{
		gc:         gc,
		Endpoint:   uri,
		HttpClient: utils.HttpClient{MaxRetries: 3},
	}, nil
}
