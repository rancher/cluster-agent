package controller

import (
	"github.com/rancher/cluster-agent/controller/authz"
	"github.com/rancher/cluster-agent/controller/healthsyncer"
	"github.com/rancher/cluster-agent/controller/nodesyncer"
	"github.com/rancher/cluster-agent/controller/statsyncer"
	"github.com/rancher/types/config"
)

func Register(workload *config.ClusterContext) {
	nodesyncer.Register(workload)
	healthsyncer.Register(workload)
	authz.Register(workload)
	statsyncer.Register(workload)
}
