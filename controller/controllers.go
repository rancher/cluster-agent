package controller

import (
	"context"

	"github.com/rancher/cluster-agent/controller/authz"
	"github.com/rancher/cluster-agent/controller/eventssyncer"
	"github.com/rancher/cluster-agent/controller/healthsyncer"
	"github.com/rancher/cluster-agent/controller/nodesyncer"
	"github.com/rancher/cluster-agent/controller/secret"
	"github.com/rancher/cluster-agent/controller/statsyncer"
	helmController "github.com/rancher/helm-controller/controller"
	"github.com/rancher/types/config"
	workloadController "github.com/rancher/workload-controller/controller"
)

func Register(ctx context.Context, cluster *config.ClusterContext) error {
	nodesyncer.Register(cluster)
	healthsyncer.Register(ctx, cluster)
	authz.Register(cluster)
	statsyncer.Register(cluster)
	eventssyncer.Register(cluster)
	secret.Register(cluster)
	helmController.Register(cluster)

	workloadContext := cluster.WorkloadContext()
	return workloadController.Register(ctx, workloadContext)
}
