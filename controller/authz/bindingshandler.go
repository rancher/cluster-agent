package authz

import (
	"context"

	"fmt"

	"strings"

	"github.com/pkg/errors"
	"github.com/rancher/cluster-agent/client"
	"github.com/rancher/cluster-agent/controller"
	authzv1 "github.com/rancher/types/apis/authorization.cattle.io/v1"
	"github.com/sirupsen/logrus"
	extv1beta1 "k8s.io/api/extensions/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func init() {
	r := &ProjectRoleTemplateBindingsHandler{}
	controller.RegisterController(r.GetName(), r)
}

type ProjectRoleTemplateBindingsHandler struct {
}

func (a *ProjectRoleTemplateBindingsHandler) GetName() string {
	return "projectRoleBindingsHandler"
}

func (a *ProjectRoleTemplateBindingsHandler) Run(ctx context.Context, clusterName string, client *client.Clients) error {
	controller := client.AuthorizationClientV1.ProjectRoleTemplateBindings("").Controller()

	rh := &roleHandler{
		clients: client,
		auth:    client.AuthorizationClientV1,
	}

	controller.AddHandler(rh.sync)
	controller.Start(1, ctx)
	return nil
}

type roleHandler struct {
	clients *client.Clients
	auth    authzv1.Interface
}

func (r *roleHandler) sync(key string, binding *authzv1.ProjectRoleTemplateBinding) error {
	if binding == nil {
		// TODO Delete
		return nil
	}

	rt, err := r.auth.ProjectRoleTemplates("").Get(binding.ProjectRoleTemplateName, metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "couldn't role template %v", binding.ProjectRoleTemplateName)
	}

	// Get namespaces belonging to project
	set := labels.Set(map[string]string{"project": binding.ProjectName})
	namespaces, err := r.clients.ClusterClientV1.Clientset.CoreV1().Namespaces().List(metav1.ListOptions{
		LabelSelector: set.AsSelector().String(),
	})
	if err != nil {
		return errors.Wrapf(err, "couldn't list namespaces with selector %s", set.AsSelector())
	}
	if len(namespaces.Items) == 0 {
		return nil
	}

	// Aggregate rules for all sub-roleTemplates
	allRules := []rbacv1.PolicyRule{}
	allRules = append(allRules, rt.Rules...)
	for _, rtName := range rt.ProjectRoleTemplates {
		subRT, err := r.auth.ProjectRoleTemplates("").Get(rtName, metav1.GetOptions{})
		if err != nil {
			return errors.Wrapf(err, "couldn't get ProjectRoleTemplate %s", rtName)
		}
		allRules = append(allRules, subRT.Rules...)
	}

	// Find any named PodSecurityPolicy resources
	pspTemplates := map[string]*authzv1.PodSecurityPolicyTemplate{}
	for _, rule := range allRules {
		foundPSP := false
		for _, resourceKind := range rule.Resources {
			if strings.EqualFold(resourceKind, "podsecuritypolicies") {
				foundPSP = true
				break
			}
		}
		if foundPSP {
			for _, resName := range rule.ResourceNames {
				pspTemplate, err := r.auth.PodSecurityPolicyTemplates("").Get(resName, metav1.GetOptions{})
				if err != nil {
					logrus.Warnf("Couldn't find PodSecurityPolicy %v. Skipping. Error: %v", resName, err)
					continue
				}
				pspTemplates[resName] = pspTemplate
			}
		}
	}

	// TODO is .Items the complete list or is there potential pagination to deal with?
	for _, ns := range namespaces.Items {
		if err := r.ensurePSPs(ns.Name, rt, allRules, binding, pspTemplates); err != nil {
			return errors.Wrapf(err, "couldn't ensure PodSecurityPolicies")
		}

		if err := r.ensureRole(ns.Name, rt, allRules, binding); err != nil {
			return errors.Wrapf(err, "couldn't ensure role %v", rt.Name)
		}

		if err := r.ensureBinding(ns.Name, rt, binding); err != nil {
			return errors.Wrapf(err, "couldn't ensure role %v", rt.Name)
		}
	}

	return nil
}

func (r *roleHandler) ensurePSPs(ns string, rt *authzv1.ProjectRoleTemplate, allRules []rbacv1.PolicyRule,
	binding *authzv1.ProjectRoleTemplateBinding, pspTemplates map[string]*authzv1.PodSecurityPolicyTemplate) error {
	pspCli := r.clients.ClusterClientV1.Clientset.ExtensionsV1beta1().PodSecurityPolicies()
	for name, pspTemplate := range pspTemplates {
		if psp, err := pspCli.Get(name, metav1.GetOptions{}); err == nil {
			psp.Spec = pspTemplate.Spec
			if _, err := pspCli.Update(psp); err != nil {
				return errors.Wrapf(err, "couldn't update PodSecurityPolicy %v", name)
			}
			continue
		}
		_, err := pspCli.Create(&extv1beta1.PodSecurityPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
			Spec: pspTemplate.Spec,
		})
		if err != nil {
			return errors.Wrapf(err, "couldn't create PodSecurityPolicy %v", name)
		}
	}

	return nil
}

func (r *roleHandler) ensureRole(ns string, rt *authzv1.ProjectRoleTemplate, allRules []rbacv1.PolicyRule,
	binding *authzv1.ProjectRoleTemplateBinding) error {
	roleCli := r.clients.ClusterClientV1.Clientset.RbacV1().Roles(ns)
	if role, err := roleCli.Get(rt.Name, metav1.GetOptions{}); err == nil {
		role.Rules = allRules
		_, err := roleCli.Update(role)
		return err
	}
	_, err := roleCli.Create(&rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name: rt.Name,
		},
		Rules: allRules,
	})
	return err
}

func (r *roleHandler) ensureBinding(ns string, rt *authzv1.ProjectRoleTemplate, binding *authzv1.ProjectRoleTemplateBinding) error {
	bindingCli := r.clients.ClusterClientV1.Clientset.RbacV1().RoleBindings(ns)
	bindingName := strings.ToLower(fmt.Sprintf("%v-%v-%v", rt.Name, binding.Subject.Kind, binding.Subject.Name))
	_, err := bindingCli.Get(bindingName, metav1.GetOptions{})
	if err == nil {
		return nil
	}

	_, err = bindingCli.Create(&rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: bindingName,
		},
		Subjects: []rbacv1.Subject{binding.Subject},
		RoleRef: rbacv1.RoleRef{
			Kind: "Role",
			Name: rt.Name,
		},
	})

	return err
}
