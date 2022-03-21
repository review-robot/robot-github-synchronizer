package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/opensourceways/community-robot-lib/githubclient"
	"github.com/opensourceways/community-robot-lib/logrusutil"
	liboptions "github.com/opensourceways/community-robot-lib/options"
	framework "github.com/opensourceways/community-robot-lib/robot-github-framework"
	"github.com/opensourceways/community-robot-lib/secret"
	"github.com/sirupsen/logrus"

	"github.com/opensourceways/robot-github-synchronizer/sync"
)

type options struct {
	syncEndpoint string

	service liboptions.ServiceOptions
	github  liboptions.GithubOptions
}

func (o *options) Validate() error {
	if o.syncEndpoint == "" {
		return fmt.Errorf("missing sync-endpoint param")
	}

	if err := o.service.Validate(); err != nil {
		return err
	}

	return o.github.Validate()
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options

	o.github.AddFlags(fs)
	o.service.AddFlags(fs)

	fs.StringVar(&o.syncEndpoint, "sync-endpoint", "", "the sync agent server api root path")

	_ = fs.Parse(args)
	return o
}

func main() {
	logrusutil.ComponentInit(botName)

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	secretAgent := new(secret.Agent)
	if err := secretAgent.Start([]string{o.github.TokenPath}); err != nil {
		logrus.WithError(err).Fatal("Error starting secret agent.")
	}

	defer secretAgent.Stop()

	c := githubclient.NewGithubClient(secretAgent.GetTokenGenerator(o.github.TokenPath))

	syncCli, err := sync.NewSynchronize(o.syncEndpoint, c)
	if err != nil {
		logrus.WithError(err).Fatal("error init synchronizer.")
	}

	v, _, err := c.Users.Get(context.Background(), "")
	if err != nil {
		logrus.WithError(err).Error("Error get bot name")
	}

	r := newRobot(syncCli, strings.ToLower(v.GetLogin()))

	framework.Run(r, o.service)
}
