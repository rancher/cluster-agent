package controller

import (
	"github.com/rancher/cluster-agent/controller/authz"
	"github.com/rancher/cluster-agent/controller/healthsyncer"
	"github.com/rancher/cluster-agent/controller/nodesyncer"
	"github.com/rancher/types/config"
)

func Register(workload *config.WorkloadContext) {
	nodesyncer.Register(workload)
	healthsyncer.Register(workload)
	authz.Register(workload)
}
