package authz

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/rancher/types/apis/management.cattle.io/v3"
	"github.com/rancher/types/config"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func newNSHandler(context *config.ClusterContext) *nsLifecycle {
	return &nsLifecycle{
		workload:      context,
		clusterLister: context.Management.Management.Clusters("").Controller().Lister(),
		clusterName:   context.ClusterName,
	}
}

type nsLifecycle struct {
	workload      *config.ClusterContext
	clusterLister v3.ClusterLister
	clusterName   string
}

func (l *nsLifecycle) sync(key string, ns *v1.Namespace) error {
	if ns.Name != "default" {
		return nil
	}

	cluster, err := l.clusterLister.Get("", l.clusterName)
	if err != nil {
		return err
	}
	if cluster == nil {
		return errors.Errorf("couldn't find cluster %v", l.clusterName)
	}

	updateCluster := false
	c, err := v3.ClusterConditionDefaultNamespaceAssigned.DoUntilTrue(cluster.DeepCopy(), func() (runtime.Object, error) {
		updateCluster = true
		projectID := ns.Annotations[projectIDAnnotation]
		if projectID != "" {
			return nil, nil
		}

		ns = ns.DeepCopy()
		if ns.Annotations == nil {
			ns.Annotations = map[string]string{}
		}
		ns.Annotations[projectIDAnnotation] = fmt.Sprintf("%v:default", l.clusterName)
		if _, err := l.workload.Core.Namespaces(l.clusterName).Update(ns); err != nil {
			return nil, err
		}

		return nil, nil
	})
	if updateCluster {
		if _, err := l.workload.Management.Management.Clusters("").ObjectClient().Update(cluster.Name, c); err != nil {
			return err
		}
	}
	return err
}
