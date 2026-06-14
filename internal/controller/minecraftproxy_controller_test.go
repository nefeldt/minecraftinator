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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	minecraftv1alpha1 "github.com/mittwald/minecraftinator/api/v1alpha1"
)

var _ = Describe("MinecraftProxy Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-proxy"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		minecraftproxy := &minecraftv1alpha1.MinecraftProxy{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind MinecraftProxy")
			err := k8sClient.Get(ctx, typeNamespacedName, minecraftproxy)
			if err != nil && errors.IsNotFound(err) {
				resource := &minecraftv1alpha1.MinecraftProxy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &minecraftv1alpha1.MinecraftProxy{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the MinecraftProxy instance")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"}}
			_ = k8sClient.Delete(ctx, dep)
			svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"}}
			_ = k8sClient.Delete(ctx, svc)
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &MinecraftProxyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking a Deployment was created")
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, dep)).To(Succeed())
			Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(dep.Spec.Template.Spec.Containers[0].Image).To(ContainSubstring("itzg/mc-router"))

			By("Checking a Service was created")
			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, svc)).To(Succeed())
			Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeLoadBalancer))
		})
	})
})
