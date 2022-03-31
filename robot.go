package main

import (
	"fmt"

	"github.com/google/go-github/v36/github"
	"github.com/opensourceways/community-robot-lib/config"
	"github.com/opensourceways/community-robot-lib/githubclient"
	"github.com/opensourceways/community-robot-lib/robot-github-framework"
	"github.com/sirupsen/logrus"

	conf "github.com/opensourceways/robot-github-synchronizer/config"
	"github.com/opensourceways/robot-github-synchronizer/sync"
)

const botName = "synchronizer"

func newRobot(sync sync.Synchronize, name string, ) *robot {
	return &robot{sync: sync, name: name}
}

type robot struct {
	sync sync.Synchronize
	name string
}

func (bot *robot) NewConfig() config.Config {
	return &conf.Configuration{}
}

func (bot *robot) getConfig(cfg config.Config, org, repo string) (*conf.BotConfig, error) {
	c, ok := cfg.(*conf.Configuration)
	if !ok {
		return nil, fmt.Errorf("can't convert to configuration")
	}

	if bc := c.ConfigFor(org, repo); bc != nil {
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
	org, repo := githubclient.GetOrgRepo(e.GetRepo())

	cfg, err := bot.getConfig(c, org, repo)
	if err != nil {
		return err
	}

	if githubclient.IsIssueOpened(e.GetAction()) {
		return bot.sync.HandleSyncIssueToGitee(org, repo, e.GetIssue(), cfg)
	}

	if e.GetAction() == githubclient.ActionReopen || e.GetAction() == githubclient.ActionClosed {
		return bot.sync.HandleSyncIssueStatus(org, repo, e.GetIssue(), cfg)
	}

	return nil
}

func (bot *robot) handleNoteEvent(e *github.IssueCommentEvent, c config.Config, log *logrus.Entry) error {
	if !githubclient.IsIssueCommentCreated(e.GetAction()) {
		return nil
	}

	org, repo := githubclient.GetOrgRepo(e.GetRepo())

	cfg, err := bot.getConfig(c, org, repo)
	if err != nil {
		return err
	}

	return bot.sync.HandleSyncIssueComment(org, repo, e, cfg)
}
