package authz

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/rancher/types/apis/management.cattle.io/v3"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (m *manager) syncNS(key string, obj *v1.Namespace) error {
	if obj == nil {
		return nil
	}

	if obj.DeletionTimestamp != nil {
		return nil
	}

	if err := m.ensureDefaultNamespaceAssigned(key, obj); err != nil {
		return err
	}

	return m.ensurePRTBAddToNamespace(key, obj)
}

func (m *manager) ensureDefaultNamespaceAssigned(key string, ns *v1.Namespace) error {
	if ns.Name != "default" {
		return nil
	}

	cluster, err := m.clusterLister.Get("", m.clusterName)
	if err != nil {
		return err
	}
	if cluster == nil {
		return errors.Errorf("couldn't find cluster %v", m.clusterName)
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
		ns.Annotations[projectIDAnnotation] = fmt.Sprintf("%v:rancher-default", m.clusterName)
		if _, err := m.workload.Core.Namespaces(m.clusterName).Update(ns); err != nil {
			return nil, err
		}

		return nil, nil
	})
	if updateCluster {
		if _, err := m.workload.Management.Management.Clusters("").ObjectClient().Update(cluster.Name, c); err != nil {
			return err
		}
	}
	return err
}

func (m *manager) ensurePRTBAddToNamespace(key string, obj *v1.Namespace) error {
	// Get project that contain this namespace
	projectID := obj.Annotations[projectIDAnnotation]
	if len(projectID) == 0 {
		return nil
	}

	prtbs, err := m.prtbIndexer.ByIndex(prtbIndex, projectID)
	if err != nil {
		return errors.Wrapf(err, "couldn't get project role binding templates associated with project id %s", projectID)
	}
	for _, prtb := range prtbs {
		prtb, ok := prtb.(*v3.ProjectRoleTemplateBinding)
		if !ok {
			return errors.Wrapf(err, "object %v is not valid project role template binding", prtb)
		}

		if prtb.RoleTemplateName == "" {
			logrus.Warnf("ProjectRoleTemplateBinding %v has no role template set. Skipping.", prtb.Name)
			continue
		}

		rt, err := m.rtLister.Get("", prtb.RoleTemplateName)
		if err != nil {
			return errors.Wrapf(err, "couldn't get role template %v", prtb.RoleTemplateName)
		}

		roles := map[string]*v3.RoleTemplate{}
		if err := m.gatherRoles(rt, roles); err != nil {
			return err
		}

		if err := m.ensureRoles(roles); err != nil {
			return errors.Wrap(err, "couldn't ensure roles")
		}

		for _, role := range roles {
			if err := m.ensureBinding(obj.Name, role.Name, prtb); err != nil {
				return errors.Wrapf(err, "couldn't ensure binding %v %v in %v", role.Name, prtb.Subject.Name, obj.Name)
			}
		}
	}
	return nil
}
