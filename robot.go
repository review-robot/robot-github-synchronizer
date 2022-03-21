package main

import (
	"fmt"
	"strings"

	"github.com/google/go-github/v36/github"
	"github.com/opensourceways/community-robot-lib/config"
	"github.com/opensourceways/community-robot-lib/githubclient"
	"github.com/opensourceways/community-robot-lib/robot-github-framework"
	"github.com/sirupsen/logrus"

	"github.com/opensourceways/robot-github-synchronizer/sync"
)

const botName = "synchronizer"

func newRobot(sync sync.Synchronizer, name string, ) *robot {
	return &robot{sync: sync, name: name}
}

type robot struct {
	sync sync.Synchronizer
	name string
}

func (bot *robot) NewConfig() config.Config {
	return &configuration{}
}

func (bot *robot) getConfig(cfg config.Config, org, repo string) (*botConfig, error) {
	c, ok := cfg.(*configuration)
	if !ok {
		return nil, fmt.Errorf("can't convert to configuration")
	}

	if bc := c.configFor(org, repo); bc != nil {
		return bc, nil
	}

	return nil, fmt.Errorf("no config for this repo:%s/%s", org, repo)
}

func (bot *robot) RegisterEventHandler(f framework.HandlerRegister) {
	f.RegisterIssueHandler(bot.handleIssueEvent)
	f.RegisterIssueCommentHandler(bot.handleNoteEvent)
}

func (bot *robot) RobotName() string {
	return botName
}

func (bot *robot) handleIssueEvent(e *github.IssuesEvent, c config.Config, log *logrus.Entry) error {
	if !githubclient.IsIssueOpened(e.GetAction()) {
		return nil
	}

	org, repo := githubclient.GetOrgRepo(e.GetRepo())

	cfg, err := bot.getConfig(c, org, repo)
	if err != nil || !cfg.EnableSyncIssue {
		return err
	}

	if !bot.needSync(cfg, e.GetIssue().GetUser().GetLogin()) {
		log.Info("not need sync")
		return nil
	}

	return bot.sync.HandleSyncIssueToGitee(org, repo, e.GetIssue())
}

func (bot *robot) handleNoteEvent(e *github.IssueCommentEvent, c config.Config, log *logrus.Entry) error {
	/*if !e.IsCreatingCommentEvent() || !e.IsIssue() {
		return nil
	}

	org, repo := e.GetOrgRepo()

	cfg, err := bot.getConfig(c, org, repo)
	if err != nil || !cfg.EnableSyncComment {
		return err
	}

	if !bot.needSync(cfg, e.GetCommenter()) {
		return nil
	}

	// TODO: exec sync logic*/
	return nil
}

func (bot *robot) needSync(cfg *botConfig, author string) bool {
	if len(cfg.DoNotSyncAuthors) == 0 {
		return strings.ToLower(author) != bot.name
	}

	for _, v := range cfg.DoNotSyncAuthors {
		if strings.ToLower(v) == botName {
			return false
		}
	}

	return true
}
