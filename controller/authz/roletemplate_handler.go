package authz

import (
	"github.com/pkg/errors"
	"github.com/rancher/types/apis/management.cattle.io/v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (m *manager) syncRT(key string, template *v3.RoleTemplate) error {
	if template == nil {
		return nil
	}

	if template.DeletionTimestamp != nil {
		return m.ensureRTDelete(key, template)
	}

	return m.ensureRT(key, template)
}

func (m *manager) ensureRT(key string, template *v3.RoleTemplate) error {
	template = template.DeepCopy()
	if m.addFinalizer(template) {
		if _, err := m.workload.Management.Management.RoleTemplates("").Update(template); err != nil {
			return errors.Wrapf(err, "couldn't set finalizer on %v", key)
		}
	}

	roles := map[string]*v3.RoleTemplate{}
	if err := m.gatherRoles(template, roles); err != nil {
		return err
	}

	if err := m.ensureRoles(roles); err != nil {
		return errors.Wrapf(err, "couldn't ensure roles")
	}

	return nil
}

func (m *manager) ensureRTDelete(key string, template *v3.RoleTemplate) error {
	if len(template.ObjectMeta.Finalizers) <= 0 || template.ObjectMeta.Finalizers[0] != m.finalizerName {
		return nil
	}

	template = template.DeepCopy()

	roles := map[string]*v3.RoleTemplate{}
	if err := m.gatherRoles(template, roles); err != nil {
		return err
	}

	roleCli := m.workload.K8sClient.RbacV1().ClusterRoles()
	for _, role := range roles {
		if err := roleCli.Delete(role.Name, &metav1.DeleteOptions{}); err != nil {
			if !apierrors.IsNotFound(err) {
				return errors.Wrapf(err, "error deleting clusterrole %v", role.Name)
			}
		}
	}

	if m.removeFinalizer(template) {
		_, err := m.workload.Management.Management.RoleTemplates("").Update(template)
		return err
	}

	return nil
}
