/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cleanupv1alpha1 "github.com/darjain/kubecleaner/api/v1alpha1"
)

var protectedNamespaces = map[string]bool{
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,
	"default":         true,
}

// KubeCleanerReconciler reconciles a KubeCleaner object.
type KubeCleanerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=cleanup.kubecleaner.io,resources=kubecleaners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cleanup.kubecleaner.io,resources=kubecleaners/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cleanup.kubecleaner.io,resources=kubecleaners/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;delete

func (r *KubeCleanerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cleaner cleanupv1alpha1.KubeCleaner
	if err := r.Get(ctx, req.NamespacedName, &cleaner); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("KubeCleaner resource not found, ignoring (likely deleted)")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	interval, err := time.ParseDuration(cleaner.Spec.Interval)
	if err != nil {
		log.Error(err, "Invalid interval", "interval", cleaner.Spec.Interval)
		r.updateStatus(ctx, &cleaner, 0, fmt.Sprintf("Invalid interval %q: %v", cleaner.Spec.Interval, err))
		return ctrl.Result{}, nil
	}

	var labelSelector labels.Selector
	if cleaner.Spec.Selector != nil {
		labelSelector, err = metav1.LabelSelectorAsSelector(cleaner.Spec.Selector)
		if err != nil {
			log.Error(err, "Invalid label selector")
			r.updateStatus(ctx, &cleaner, 0, fmt.Sprintf("Invalid selector: %v", err))
			return ctrl.Result{}, nil
		}
	}

	operatorNamespace := getOperatorNamespace()

	log.Info("Starting cleanup run",
		"interval", cleaner.Spec.Interval,
		"dryRun", cleaner.Spec.DryRun,
		"operatorNamespace", operatorNamespace,
	)

	totalDeleted := int32(0)
	var cleanupErrors []error

	totalDeleted += r.cleanupPods(ctx, log, &cleaner, labelSelector, operatorNamespace, &cleanupErrors)
	totalDeleted += r.cleanupDeployments(ctx, log, &cleaner, labelSelector, operatorNamespace, &cleanupErrors)
	totalDeleted += r.cleanupServices(ctx, log, &cleaner, labelSelector, operatorNamespace, &cleanupErrors)
	totalDeleted += r.cleanupIngresses(ctx, log, &cleaner, labelSelector, operatorNamespace, &cleanupErrors)
	totalDeleted += r.cleanupPersistentVolumes(ctx, log, &cleaner, labelSelector, &cleanupErrors)
	totalDeleted += r.cleanupNamespaces(ctx, log, &cleaner, labelSelector, operatorNamespace, &cleanupErrors)

	message := fmt.Sprintf("Cleanup completed. Deleted %d resources.", totalDeleted)
	if cleaner.Spec.DryRun {
		message = fmt.Sprintf("Dry run completed. Would delete %d resources.", totalDeleted)
	}
	if len(cleanupErrors) > 0 {
		message += fmt.Sprintf(" Encountered %d errors.", len(cleanupErrors))
	}

	r.updateStatus(ctx, &cleaner, totalDeleted, message)

	log.Info("Reconciliation complete, scheduling next run", "requeueAfter", interval, "deleted", totalDeleted)
	return ctrl.Result{RequeueAfter: interval}, nil
}

func (r *KubeCleanerReconciler) cleanupPods(
	ctx context.Context,
	log logr.Logger,
	cleaner *cleanupv1alpha1.KubeCleaner,
	labelSelector labels.Selector,
	operatorNamespace string,
	errors *[]error,
) int32 {
	var podList corev1.PodList
	listOpts := buildListOptions(labelSelector)

	if err := r.List(ctx, &podList, listOpts...); err != nil {
		log.Error(err, "Failed to list Pods")
		*errors = append(*errors, err)
		return 0
	}

	deleted := int32(0)
	for i := range podList.Items {
		pod := &podList.Items[i]

		if isProtectedNamespace(pod.Namespace, operatorNamespace) {
			continue
		}
		if shouldExclude(pod.Labels, cleaner.Spec.ExcludeLabels) {
			continue
		}

		if cleaner.Spec.DryRun {
			log.Info("[DRY RUN] Would delete Pod", "name", pod.Name, "namespace", pod.Namespace)
			deleted++
			continue
		}

		log.Info("Deleting Pod", "name", pod.Name, "namespace", pod.Namespace)
		if err := r.Delete(ctx, pod, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to delete Pod", "name", pod.Name, "namespace", pod.Namespace)
				*errors = append(*errors, err)
			}
			continue
		}
		deleted++
	}
	return deleted
}

func (r *KubeCleanerReconciler) cleanupDeployments(
	ctx context.Context,
	log logr.Logger,
	cleaner *cleanupv1alpha1.KubeCleaner,
	labelSelector labels.Selector,
	operatorNamespace string,
	errors *[]error,
) int32 {
	var deploymentList appsv1.DeploymentList
	listOpts := buildListOptions(labelSelector)

	if err := r.List(ctx, &deploymentList, listOpts...); err != nil {
		log.Error(err, "Failed to list Deployments")
		*errors = append(*errors, err)
		return 0
	}

	deleted := int32(0)
	for i := range deploymentList.Items {
		deployment := &deploymentList.Items[i]

		if isProtectedNamespace(deployment.Namespace, operatorNamespace) {
			continue
		}
		if shouldExclude(deployment.Labels, cleaner.Spec.ExcludeLabels) {
			continue
		}

		if cleaner.Spec.DryRun {
			log.Info("[DRY RUN] Would delete Deployment", "name", deployment.Name, "namespace", deployment.Namespace)
			deleted++
			continue
		}

		log.Info("Deleting Deployment", "name", deployment.Name, "namespace", deployment.Namespace)
		if err := r.Delete(ctx, deployment, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to delete Deployment", "name", deployment.Name, "namespace", deployment.Namespace)
				*errors = append(*errors, err)
			}
			continue
		}
		deleted++
	}
	return deleted
}

func (r *KubeCleanerReconciler) cleanupServices(
	ctx context.Context,
	log logr.Logger,
	cleaner *cleanupv1alpha1.KubeCleaner,
	labelSelector labels.Selector,
	operatorNamespace string,
	errors *[]error,
) int32 {
	var serviceList corev1.ServiceList
	listOpts := buildListOptions(labelSelector)

	if err := r.List(ctx, &serviceList, listOpts...); err != nil {
		log.Error(err, "Failed to list Services")
		*errors = append(*errors, err)
		return 0
	}

	deleted := int32(0)
	for i := range serviceList.Items {
		service := &serviceList.Items[i]

		if isProtectedNamespace(service.Namespace, operatorNamespace) {
			continue
		}
		if shouldExclude(service.Labels, cleaner.Spec.ExcludeLabels) {
			continue
		}

		if cleaner.Spec.DryRun {
			log.Info("[DRY RUN] Would delete Service", "name", service.Name, "namespace", service.Namespace)
			deleted++
			continue
		}

		log.Info("Deleting Service", "name", service.Name, "namespace", service.Namespace)
		if err := r.Delete(ctx, service); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to delete Service", "name", service.Name, "namespace", service.Namespace)
				*errors = append(*errors, err)
			}
			continue
		}
		deleted++
	}
	return deleted
}

func (r *KubeCleanerReconciler) cleanupIngresses(
	ctx context.Context,
	log logr.Logger,
	cleaner *cleanupv1alpha1.KubeCleaner,
	labelSelector labels.Selector,
	operatorNamespace string,
	errors *[]error,
) int32 {
	var ingressList networkingv1.IngressList
	listOpts := buildListOptions(labelSelector)

	if err := r.List(ctx, &ingressList, listOpts...); err != nil {
		log.Error(err, "Failed to list Ingresses")
		*errors = append(*errors, err)
		return 0
	}

	deleted := int32(0)
	for i := range ingressList.Items {
		ingress := &ingressList.Items[i]

		if isProtectedNamespace(ingress.Namespace, operatorNamespace) {
			continue
		}
		if shouldExclude(ingress.Labels, cleaner.Spec.ExcludeLabels) {
			continue
		}

		if cleaner.Spec.DryRun {
			log.Info("[DRY RUN] Would delete Ingress", "name", ingress.Name, "namespace", ingress.Namespace)
			deleted++
			continue
		}

		log.Info("Deleting Ingress", "name", ingress.Name, "namespace", ingress.Namespace)
		if err := r.Delete(ctx, ingress); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to delete Ingress", "name", ingress.Name, "namespace", ingress.Namespace)
				*errors = append(*errors, err)
			}
			continue
		}
		deleted++
	}
	return deleted
}

func (r *KubeCleanerReconciler) cleanupPersistentVolumes(
	ctx context.Context,
	log logr.Logger,
	cleaner *cleanupv1alpha1.KubeCleaner,
	labelSelector labels.Selector,
	errors *[]error,
) int32 {
	var pvList corev1.PersistentVolumeList
	listOpts := buildListOptions(labelSelector)

	if err := r.List(ctx, &pvList, listOpts...); err != nil {
		log.Error(err, "Failed to list PersistentVolumes")
		*errors = append(*errors, err)
		return 0
	}

	deleted := int32(0)
	for i := range pvList.Items {
		pv := &pvList.Items[i]

		if shouldExclude(pv.Labels, cleaner.Spec.ExcludeLabels) {
			continue
		}

		if cleaner.Spec.DryRun {
			log.Info("[DRY RUN] Would delete PersistentVolume", "name", pv.Name)
			deleted++
			continue
		}

		log.Info("Deleting PersistentVolume", "name", pv.Name)
		if err := r.Delete(ctx, pv); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to delete PersistentVolume", "name", pv.Name)
				*errors = append(*errors, err)
			}
			continue
		}
		deleted++
	}
	return deleted
}

func (r *KubeCleanerReconciler) cleanupNamespaces(
	ctx context.Context,
	log logr.Logger,
	cleaner *cleanupv1alpha1.KubeCleaner,
	labelSelector labels.Selector,
	operatorNamespace string,
	errors *[]error,
) int32 {
	var nsList corev1.NamespaceList
	listOpts := buildListOptions(labelSelector)

	if err := r.List(ctx, &nsList, listOpts...); err != nil {
		log.Error(err, "Failed to list Namespaces")
		*errors = append(*errors, err)
		return 0
	}

	deleted := int32(0)
	for i := range nsList.Items {
		ns := &nsList.Items[i]

		if isProtectedNamespace(ns.Name, operatorNamespace) {
			continue
		}
		if shouldExclude(ns.Labels, cleaner.Spec.ExcludeLabels) {
			continue
		}

		if cleaner.Spec.DryRun {
			log.Info("[DRY RUN] Would delete Namespace", "name", ns.Name)
			deleted++
			continue
		}

		log.Info("Deleting Namespace", "name", ns.Name)
		if err := r.Delete(ctx, ns); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "Failed to delete Namespace", "name", ns.Name)
				*errors = append(*errors, err)
			}
			continue
		}
		deleted++
	}
	return deleted
}

func (r *KubeCleanerReconciler) updateStatus(
	ctx context.Context,
	cleaner *cleanupv1alpha1.KubeCleaner,
	deletedCount int32,
	message string,
) {
	log := logf.FromContext(ctx)

	latest := &cleanupv1alpha1.KubeCleaner{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(cleaner), latest); err != nil {
		log.Error(err, "Failed to re-fetch KubeCleaner for status update")
		return
	}

	now := metav1.Now()
	latest.Status.LastCleanupTime = &now
	latest.Status.DeletedCount = deletedCount
	latest.Status.Message = message

	if err := r.Status().Update(ctx, latest); err != nil {
		log.Error(err, "Failed to update KubeCleaner status")
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *KubeCleanerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cleanupv1alpha1.KubeCleaner{}).
		Named("kubecleaner").
		Complete(r)
}

func getOperatorNamespace() string {
	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil {
		return strings.TrimSpace(string(nsBytes))
	}
	if ns := os.Getenv("OPERATOR_NAMESPACE"); ns != "" {
		return ns
	}
	return "kubecleaner-system"
}

func isProtectedNamespace(namespace, operatorNamespace string) bool {
	return protectedNamespaces[namespace] || namespace == operatorNamespace
}

func shouldExclude(resourceLabels map[string]string, excludeLabels map[string]string) bool {
	for key, value := range excludeLabels {
		if resourceLabels[key] == value {
			return true
		}
	}
	return false
}

func buildListOptions(labelSelector labels.Selector) []client.ListOption {
	if labelSelector != nil {
		return []client.ListOption{client.MatchingLabelsSelector{Selector: labelSelector}}
	}
	return nil
}
