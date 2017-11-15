package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/cluster-agent/client"
)

const (
	ResyncPeriod = 1 * time.Minute
)

type Controller interface {
	GetName() string
	Run(ctx context.Context, clusterName string, client *client.Clients) error
}

var (
	controllers map[string]Controller
)

func GetControllers() map[string]Controller {
	return controllers
}

func RegisterController(name string, controller Controller) error {
	if controllers == nil {
		controllers = make(map[string]Controller)
	}
	if _, exists := controllers[name]; exists {
		return fmt.Errorf("controller already registered")
	}
	controllers[name] = controller
	return nil
}
