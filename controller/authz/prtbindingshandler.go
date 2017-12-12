package authz

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	typescorev1 "github.com/rancher/types/apis/core/v1"
	typesextv1beta1 "github.com/rancher/types/apis/extensions/v1beta1"
	"github.com/rancher/types/apis/management.cattle.io/v3"
	typesrbacv1 "github.com/rancher/types/apis/rbac.authorization.k8s.io/v1"
	"github.com/rancher/types/config"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	bindingNameLabel = "io.cattle.field.projectRoleTemplateBindingName"
	projectIDLabel   = "io.cattle.field.projectId"
)

func Register(workload *config.ClusterContext) {
	r := &roleHandler{
		workload:   workload,
		rtLister:   workload.Management.Management.RoleTemplates("").Controller().Lister(),
		psptLister: workload.Management.Management.PodSecurityPolicyTemplates("").Controller().Lister(),
		nsLister:   workload.Core.Namespaces("").Controller().Lister(),
		rbLister:   workload.RBAC.RoleBindings("").Controller().Lister(),
		crbLister:  workload.RBAC.ClusterRoleBindings("").Controller().Lister(),
		crLister:   workload.RBAC.ClusterRoles("").Controller().Lister(),
		pspLister:  workload.Extensions.PodSecurityPolicies("").Controller().Lister(),
	}
	workload.Management.Management.ProjectRoleTemplateBindings("").Controller().AddHandler(r.syncPRTB)
}

type roleHandler struct {
	workload   *config.ClusterContext
	rtLister   v3.RoleTemplateLister
	psptLister v3.PodSecurityPolicyTemplateLister
	nsLister   typescorev1.NamespaceLister
	crLister   typesrbacv1.ClusterRoleLister
	crbLister  typesrbacv1.ClusterRoleBindingLister
	rbLister   typesrbacv1.RoleBindingLister
	pspLister  typesextv1beta1.PodSecurityPolicyLister
}

func (r *roleHandler) syncPRTB(key string, binding *v3.ProjectRoleTemplateBinding) error {
	if binding.ObjectMeta.DeletionTimestamp == nil {
		return r.ensurePRTB(key, binding)
	}

	// TODO delete
	return nil
}

func (r *roleHandler) ensurePRTB(key string, binding *v3.ProjectRoleTemplateBinding) error {
	rt, err := r.rtLister.Get("", binding.RoleTemplateName)
	if err != nil {
		return errors.Wrapf(err, "couldn't get role template %v", binding.RoleTemplateName)
	}

	// Get namespaces belonging to project
	set := labels.Set(map[string]string{projectIDLabel: binding.ProjectName})
	namespaces, err := r.nsLister.List("", set.AsSelector())
	if err != nil {
		return errors.Wrapf(err, "couldn't list namespaces with selector %s", set.AsSelector())
	}
	if len(namespaces) == 0 {
		return nil
	}

	roles := map[string]*v3.RoleTemplate{}

	if err := r.gatherRoles(rt, roles); err != nil {
		return err
	}

	if err := r.ensureRoles(roles); err != nil {
		return errors.Wrap(err, "couldn't ensure roles")
	}

	// TODO is .Items the complete list or is there potential pagination to deal with?
	for _, ns := range namespaces {
		if err := r.ensureBinding(ns.Name, rt.Name, binding); err != nil {
			return errors.Wrapf(err, "couldn't ensure binding %v %v in %v", rt.Name, binding.Subject.Name, ns.Name)
		}
	}

	return nil
}

func (r *roleHandler) gatherRoles(rt *v3.RoleTemplate, roleTemplates map[string]*v3.RoleTemplate) error {
	roleTemplates[rt.Name] = rt

	for _, rtName := range rt.RoleTemplateNames {
		subRT, err := r.rtLister.Get("", rtName)
		if err != nil {
			return errors.Wrapf(err, "couldn't get RoleTemplate %s", rtName)
		}
		if err := r.gatherRoles(subRT, roleTemplates); err != nil {
			return errors.Wrapf(err, "couldn't gather RoleTemplate %s", rtName)
		}
	}

	return nil
}

func (r *roleHandler) ensureRoles(rts map[string]*v3.RoleTemplate) error {
	roleCli := r.workload.K8sClient.RbacV1().ClusterRoles()
	for _, rt := range rts {
		if rt.Builtin {
			// TODO assert the role exists and log an error if it doesnt.
			continue
		}

		if role, err := r.crLister.Get("", rt.Name); err == nil {
			// TODO potentially check a version so that we don't do unnecessary updates
			role.Rules = rt.Rules
			_, err := roleCli.Update(role)
			if err != nil {
				return errors.Wrapf(err, "couldn't update role %v", rt.Name)
			}
		}

		_, err := roleCli.Create(&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: rt.Name,
			},
			Rules: rt.Rules,
		})
		if err != nil {
			return errors.Wrapf(err, "couldn't create role %v", rt.Name)
		}
	}

	return nil
}

func (r *roleHandler) ensureBinding(ns, roleName string, binding *v3.ProjectRoleTemplateBinding) error {
	bindingCli := r.workload.K8sClient.RbacV1().RoleBindings(ns)
	// TODO dont use name, use a UID
	bindingName := strings.ToLower(fmt.Sprintf("%v-%v-%v", roleName, binding.Subject.Kind, binding.Subject.Name))
	_, err := r.rbLister.Get(ns, bindingName)
	if err == nil {
		return nil
	}

	_, err = bindingCli.Create(&rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   bindingName,
			Labels: map[string]string{bindingNameLabel: binding.Name},
		},
		Subjects: []rbacv1.Subject{binding.Subject},
		RoleRef: rbacv1.RoleRef{
			Kind: "ClusterRole",
			Name: roleName,
		},
	})

	return err
}
