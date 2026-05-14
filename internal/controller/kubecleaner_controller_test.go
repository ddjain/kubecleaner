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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cleanupv1alpha1 "github.com/darjain/kubecleaner/api/v1alpha1"
)

var testCounter int

var _ = Describe("KubeCleaner Controller", func() {
	const (
		cleanerNamespace = "default"
	)

	ctx := context.Background()

	var (
		reconciler  *KubeCleanerReconciler
		testNS      string
		cleanerName string
	)

	BeforeEach(func() {
		testCounter++
		testNS = fmt.Sprintf("test-cleanup-%d", testCounter)
		cleanerName = fmt.Sprintf("test-cleaner-%d", testCounter)

		reconciler = &KubeCleanerReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNS,
			},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	})

	AfterEach(func() {
		cleaner := &cleanupv1alpha1.KubeCleaner{}
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      cleanerName,
			Namespace: cleanerNamespace,
		}, cleaner)
		if err == nil {
			Expect(k8sClient.Delete(ctx, cleaner)).To(Succeed())
		}
	})

	createCleaner := func(spec cleanupv1alpha1.KubeCleanerSpec) *cleanupv1alpha1.KubeCleaner {
		cleaner := &cleanupv1alpha1.KubeCleaner{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cleanerName,
				Namespace: cleanerNamespace,
			},
			Spec: spec,
		}
		Expect(k8sClient.Create(ctx, cleaner)).To(Succeed())
		return cleaner
	}

	createPod := func(name string, podLabels map[string]string) *corev1.Pod {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNS,
				Labels:    podLabels,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test",
						Image: "nginx:latest",
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())
		return pod
	}

	createDeployment := func(name string, depLabels map[string]string) *appsv1.Deployment {
		replicas := int32(1)
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNS,
				Labels:    depLabels,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": name},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": name},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name:  "test",
								Image: "nginx:latest",
							},
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
		return deployment
	}

	reconcileRequest := func() reconcile.Result {
		result, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      cleanerName,
				Namespace: cleanerNamespace,
			},
		})
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	Context("When the KubeCleaner resource does not exist", func() {
		It("should return cleanly without error", func() {
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "nonexistent",
					Namespace: cleanerNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})
	})

	Context("When interval is valid", func() {
		It("should requeue after the specified interval", func() {
			createCleaner(cleanupv1alpha1.KubeCleanerSpec{
				Interval: "10m",
				DryRun:   true,
			})

			result := reconcileRequest()
			Expect(result.RequeueAfter).To(Equal(10 * time.Minute))
		})
	})

	Context("When dryRun is true", func() {
		It("should not actually delete pods", func() {
			createPod("dry-run-pod", map[string]string{"app": "test"})

			createCleaner(cleanupv1alpha1.KubeCleanerSpec{
				Interval: "5m",
				DryRun:   true,
			})

			reconcileRequest()

			var pod corev1.Pod
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "dry-run-pod",
				Namespace: testNS,
			}, &pod)
			Expect(err).NotTo(HaveOccurred())

			var updated cleanupv1alpha1.KubeCleaner
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      cleanerName,
				Namespace: cleanerNamespace,
			}, &updated)).To(Succeed())
			Expect(updated.Status.Message).To(ContainSubstring("Dry run"))
			Expect(updated.Status.DeletedCount).To(BeNumerically(">", 0))
		})
	})

	Context("When dryRun is false", func() {
		It("should delete pods in non-protected namespaces", func() {
			createPod("delete-me-pod", map[string]string{"app": "test"})

			createCleaner(cleanupv1alpha1.KubeCleanerSpec{
				Interval: "5m",
				DryRun:   false,
			})

			reconcileRequest()

			var pod corev1.Pod
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "delete-me-pod",
				Namespace: testNS,
			}, &pod)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("should delete deployments in non-protected namespaces", func() {
			createDeployment("delete-me-deploy", map[string]string{"app": "test"})

			createCleaner(cleanupv1alpha1.KubeCleanerSpec{
				Interval: "5m",
				DryRun:   false,
			})

			reconcileRequest()

			var dep appsv1.Deployment
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "delete-me-deploy",
				Namespace: testNS,
			}, &dep)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})

	Context("When excludeLabels is set", func() {
		It("should skip pods with matching exclude labels", func() {
			createPod("protected-pod", map[string]string{"protected": "true"})
			createPod("unprotected-pod", map[string]string{"app": "test"})

			createCleaner(cleanupv1alpha1.KubeCleanerSpec{
				Interval: "5m",
				DryRun:   false,
				ExcludeLabels: map[string]string{
					"protected": "true",
				},
			})

			reconcileRequest()

			var protectedPod corev1.Pod
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "protected-pod",
				Namespace: testNS,
			}, &protectedPod)
			Expect(err).NotTo(HaveOccurred())

			var unprotectedPod corev1.Pod
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      "unprotected-pod",
				Namespace: testNS,
			}, &unprotectedPod)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})

	Context("When selector is set", func() {
		It("should only target pods matching the selector", func() {
			createPod("targeted-pod", map[string]string{"environment": "staging"})
			createPod("safe-pod", map[string]string{"environment": "production"})

			createCleaner(cleanupv1alpha1.KubeCleanerSpec{
				Interval: "5m",
				DryRun:   false,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"environment": "staging",
					},
				},
			})

			reconcileRequest()

			var targetedPod corev1.Pod
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "targeted-pod",
				Namespace: testNS,
			}, &targetedPod)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))

			var safePod corev1.Pod
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      "safe-pod",
				Namespace: testNS,
			}, &safePod)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Protected namespace enforcement", func() {
		It("should never delete pods in kube-system", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("system-pod-%d", testCounter),
					Namespace: "kube-system",
					Labels:    map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "nginx:latest",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())

			createCleaner(cleanupv1alpha1.KubeCleanerSpec{
				Interval: "5m",
				DryRun:   false,
			})

			reconcileRequest()

			var systemPod corev1.Pod
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      pod.Name,
				Namespace: "kube-system",
			}, &systemPod)
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Delete(ctx, &systemPod)).To(Succeed())
		})
	})

	Context("Status updates", func() {
		It("should update lastCleanupTime after a run", func() {
			createCleaner(cleanupv1alpha1.KubeCleanerSpec{
				Interval: "5m",
				DryRun:   true,
			})

			reconcileRequest()

			var updated cleanupv1alpha1.KubeCleaner
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      cleanerName,
				Namespace: cleanerNamespace,
			}, &updated)).To(Succeed())
			Expect(updated.Status.LastCleanupTime).NotTo(BeNil())
			Expect(updated.Status.Message).NotTo(BeEmpty())
		})
	})
})

var _ = Describe("Helper Functions", func() {
	Context("isProtectedNamespace", func() {
		It("should protect kube-system", func() {
			Expect(isProtectedNamespace("kube-system", "operator-ns")).To(BeTrue())
		})
		It("should protect kube-public", func() {
			Expect(isProtectedNamespace("kube-public", "operator-ns")).To(BeTrue())
		})
		It("should protect kube-node-lease", func() {
			Expect(isProtectedNamespace("kube-node-lease", "operator-ns")).To(BeTrue())
		})
		It("should protect default", func() {
			Expect(isProtectedNamespace("default", "operator-ns")).To(BeTrue())
		})
		It("should protect operator namespace", func() {
			Expect(isProtectedNamespace("my-operator-ns", "my-operator-ns")).To(BeTrue())
		})
		It("should not protect arbitrary namespaces", func() {
			Expect(isProtectedNamespace("user-ns", "operator-ns")).To(BeFalse())
		})
	})

	Context("shouldExclude", func() {
		It("should exclude when label matches", func() {
			resourceLabels := map[string]string{"protected": "true", "app": "test"}
			excludeLabels := map[string]string{"protected": "true"}
			Expect(shouldExclude(resourceLabels, excludeLabels)).To(BeTrue())
		})
		It("should not exclude when label key matches but value differs", func() {
			resourceLabels := map[string]string{"protected": "false"}
			excludeLabels := map[string]string{"protected": "true"}
			Expect(shouldExclude(resourceLabels, excludeLabels)).To(BeFalse())
		})
		It("should not exclude when no labels match", func() {
			resourceLabels := map[string]string{"app": "test"}
			excludeLabels := map[string]string{"protected": "true"}
			Expect(shouldExclude(resourceLabels, excludeLabels)).To(BeFalse())
		})
		It("should not exclude when excludeLabels is empty", func() {
			resourceLabels := map[string]string{"app": "test"}
			Expect(shouldExclude(resourceLabels, nil)).To(BeFalse())
		})
		It("should not exclude when resource has no labels", func() {
			excludeLabels := map[string]string{"protected": "true"}
			Expect(shouldExclude(nil, excludeLabels)).To(BeFalse())
		})
	})
})
