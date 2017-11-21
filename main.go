package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	client "github.com/rancher/cluster-agent/client"
	controller "github.com/rancher/cluster-agent/controller"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/sync/errgroup"
)

func main() {
	app := cli.NewApp()
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "cluster-manager-config",
			Usage: "Kube config for accessing cluster manager",
		},
		cli.StringFlag{
			Name:  "cluster-config",
			Usage: "Kube config for accessing cluster",
		},
		cli.StringFlag{
			Name:  "cluster-name",
			Usage: "name of the cluster",
		},
		cli.StringSliceFlag{
			Name:  "controller",
			Usage: "name of controllers to run. Leave blank for all controllers",
		},
	}

	app.Action = func(c *cli.Context) error {
		controllers := map[string]bool{}
		for _, c := range c.StringSlice("controller") {
			controllers[c] = true
		}

		runControllers(c.String("cluster-manager-config"), c.String("cluster-config"), c.String("cluster-name"), controllers)
		return nil
	}
	app.Run(os.Args)
}

func runControllers(clusterManagerCfg string, clusterCfg string, clusterName string, controllers map[string]bool) {
	logrus.Info("Staring cluster manager")
	ctx, cancel := context.WithCancel(context.Background())
	wg, ctx := errgroup.WithContext(ctx)

	client, err := client.NewClientSetV1(clusterManagerCfg, clusterCfg)
	if err != nil {
		logrus.Fatalf("Failed to build configs %v", err)
	}
	for name := range controller.GetControllers() {
		if len(controllers) == 0 || controllers[name] {
			logrus.Infof("Starting [%s] handler", name)
			c := controller.GetControllers()[name]
			wg.Go(func() error { return c.Run(ctx, clusterName, client) })
		}
	}

	term := make(chan os.Signal)
	signal.Notify(term, os.Interrupt, syscall.SIGTERM)

	select {
	case <-term:
		logrus.Infof("Received SIGTERM, shutting down")
	case <-ctx.Done():
	}

	cancel()

	if err := wg.Wait(); err != nil {
		logrus.Errorf("Unhandled error received, shutting down: [%v]", err)
		os.Exit(1)
	}
	os.Exit(0)
}
