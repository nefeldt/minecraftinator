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
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	minecraftv1alpha1 "github.com/mittwald/minecraftinator/api/v1alpha1"
)

const (
	defaultRouterImage = "itzg/mc-router:latest"
	proxyFinalizerName = "minecraft.mittwald.de/proxy-finalizer"

	// externalDNSHostnameAnnotation is read by ExternalDNS to create DNS records.
	externalDNSHostnameAnnotation = "external-dns.alpha.kubernetes.io/hostname"
)

// MinecraftProxyReconciler reconciles a MinecraftProxy object
type MinecraftProxyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=minecraft.mittwald.de,resources=minecraftproxies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=minecraft.mittwald.de,resources=minecraftproxies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=minecraft.mittwald.de,resources=minecraftproxies/finalizers,verbs=update
// +kubebuilder:rbac:groups=minecraft.mittwald.de,resources=minecraftservers,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *MinecraftProxyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	proxy := &minecraftv1alpha1.MinecraftProxy{}
	if err := r.Get(ctx, req.NamespacedName, proxy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !proxy.DeletionTimestamp.IsZero() {
		controllerutil.RemoveFinalizer(proxy, proxyFinalizerName)
		return ctrl.Result{}, r.Update(ctx, proxy)
	}

	if !controllerutil.ContainsFinalizer(proxy, proxyFinalizerName) {
		controllerutil.AddFinalizer(proxy, proxyFinalizerName)
		if err := r.Update(ctx, proxy); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Build the full routing table from auto-discovered + manually specified routes.
	activeRoutes, err := r.buildRoutes(ctx, proxy)
	if err != nil {
		log.Error(err, "failed to build routing table")
		return ctrl.Result{}, err
	}

	if err := r.reconcileRouterDeployment(ctx, proxy, activeRoutes); err != nil {
		log.Error(err, "failed to reconcile router Deployment")
		return ctrl.Result{}, err
	}

	if err := r.reconcileRouterService(ctx, proxy, activeRoutes); err != nil {
		log.Error(err, "failed to reconcile router Service")
		return ctrl.Result{}, err
	}

	proxy.Status.Phase = "Running"
	proxy.Status.ActiveRoutes = activeRoutes
	return ctrl.Result{}, r.Status().Update(ctx, proxy)
}

// buildRoutes discovers routes from MinecraftServer resources and merges them with
// any manual routes defined in the proxy spec. Auto-discovered routes take precedence.
func (r *MinecraftProxyReconciler) buildRoutes(ctx context.Context, proxy *minecraftv1alpha1.MinecraftProxy) ([]minecraftv1alpha1.ActiveRoute, error) {
	serverList := &minecraftv1alpha1.MinecraftServerList{}
	if err := r.List(ctx, serverList, client.InNamespace(proxy.Namespace)); err != nil {
		return nil, err
	}

	// auto-discovered routes keyed by host (host → route)
	routes := make(map[string]minecraftv1alpha1.ActiveRoute)

	for _, server := range serverList.Items {
		if server.Status.AssignedDomain == "" {
			continue // domain not assigned yet
		}
		proxyRef := server.Spec.ProxyRef
		if proxyRef == "" {
			proxyRef = "proxy"
		}
		if proxyRef != proxy.Name {
			continue
		}

		svc := &corev1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Name: server.Name, Namespace: proxy.Namespace}, svc); err != nil {
			// Service not ready yet; retry on next event.
			return nil, fmt.Errorf("server %q: service not found: %w", server.Name, err)
		}

		routes[server.Status.AssignedDomain] = minecraftv1alpha1.ActiveRoute{
			Host:      server.Status.AssignedDomain,
			ServerRef: server.Name,
			Backend:   fmt.Sprintf("%s.%s.svc.cluster.local:%d", svc.Name, svc.Namespace, minecraftPort),
			Source:    "auto",
		}
	}

	// Merge manual routes for hosts not already covered by auto-discovery.
	for _, manual := range proxy.Spec.Routes {
		if _, exists := routes[manual.Host]; exists {
			continue // auto-discovered takes precedence
		}
		svc := &corev1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Name: manual.ServerRef, Namespace: proxy.Namespace}, svc); err != nil {
			return nil, fmt.Errorf("manual route %q: service %q not found: %w", manual.Host, manual.ServerRef, err)
		}
		routes[manual.Host] = minecraftv1alpha1.ActiveRoute{
			Host:      manual.Host,
			ServerRef: manual.ServerRef,
			Backend:   fmt.Sprintf("%s.%s.svc.cluster.local:%d", svc.Name, svc.Namespace, minecraftPort),
			Source:    "manual",
		}
	}

	// Return sorted for stable Deployment args (prevents unnecessary rollouts).
	result := make([]minecraftv1alpha1.ActiveRoute, 0, len(routes))
	for _, r := range routes {
		result = append(result, r)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Host < result[j].Host })
	return result, nil
}

func (r *MinecraftProxyReconciler) reconcileRouterDeployment(ctx context.Context, proxy *minecraftv1alpha1.MinecraftProxy, routes []minecraftv1alpha1.ActiveRoute) error {
	desired := r.routerDeploymentFor(proxy, routes)
	if err := controllerutil.SetControllerReference(proxy, desired, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: proxy.Name, Namespace: proxy.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	return r.Update(ctx, existing)
}

func (r *MinecraftProxyReconciler) reconcileRouterService(ctx context.Context, proxy *minecraftv1alpha1.MinecraftProxy, routes []minecraftv1alpha1.ActiveRoute) error {
	labels := proxyLabelsFor(proxy)
	svcType := corev1.ServiceTypeLoadBalancer
	if proxy.Spec.ServiceType != "" {
		svcType = proxy.Spec.ServiceType
	}

	// Collect all hostnames for ExternalDNS annotation.
	hosts := make([]string, 0, len(routes))
	for _, route := range routes {
		hosts = append(hosts, route.Host)
	}

	annotations := map[string]string{}
	if len(hosts) > 0 {
		annotations[externalDNSHostnameAnnotation] = strings.Join(hosts, ",")
	}

	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        proxy.Name,
			Namespace:   proxy.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.ServiceSpec{
			Type:     svcType,
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:       "minecraft",
					Port:       minecraftPort,
					TargetPort: intstr.FromInt32(minecraftPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(proxy, desired, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: proxy.Name, Namespace: proxy.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Spec.Type = desired.Spec.Type
	existing.Spec.Ports = desired.Spec.Ports
	existing.Labels = desired.Labels
	if existing.Annotations == nil {
		existing.Annotations = map[string]string{}
	}
	existing.Annotations[externalDNSHostnameAnnotation] = annotations[externalDNSHostnameAnnotation]
	return r.Update(ctx, existing)
}

func (r *MinecraftProxyReconciler) routerDeploymentFor(proxy *minecraftv1alpha1.MinecraftProxy, routes []minecraftv1alpha1.ActiveRoute) *appsv1.Deployment {
	labels := proxyLabelsFor(proxy)
	replicas := int32(1)
	image := defaultRouterImage
	if proxy.Spec.Image != nil {
		image = *proxy.Spec.Image
	}

	// Build comma-separated "host=backend" pairs for mc-router --mapping.
	pairs := make([]string, 0, len(routes))
	for _, route := range routes {
		pairs = append(pairs, fmt.Sprintf("%s=%s", route.Host, route.Backend))
	}

	args := []string{}
	if len(pairs) > 0 {
		args = append(args, "--mapping", strings.Join(pairs, ","))
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxy.Name,
			Namespace: proxy.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "mc-router",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            args,
							Ports: []corev1.ContainerPort{
								{Name: "minecraft", ContainerPort: minecraftPort, Protocol: corev1.ProtocolTCP},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt32(minecraftPort),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
						},
					},
				},
			},
		},
	}
}

func proxyLabelsFor(proxy *minecraftv1alpha1.MinecraftProxy) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "mc-router",
		"app.kubernetes.io/instance":   proxy.Name,
		"app.kubernetes.io/managed-by": "minecraftinator",
	}
}

// findProxiesForServer maps a MinecraftServer change event to the MinecraftProxy it targets.
// This triggers the proxy to re-reconcile whenever a server's domain or proxyRef changes.
func (r *MinecraftProxyReconciler) findProxiesForServer(ctx context.Context, obj client.Object) []reconcile.Request {
	server, ok := obj.(*minecraftv1alpha1.MinecraftServer)
	if !ok || server.Status.AssignedDomain == "" {
		return nil
	}
	proxyRef := server.Spec.ProxyRef
	if proxyRef == "" {
		proxyRef = "proxy"
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: proxyRef, Namespace: server.Namespace}},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *MinecraftProxyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&minecraftv1alpha1.MinecraftProxy{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		// Re-reconcile the proxy whenever a MinecraftServer with a domain changes.
		Watches(
			&minecraftv1alpha1.MinecraftServer{},
			handler.EnqueueRequestsFromMapFunc(r.findProxiesForServer),
		).
		Named("minecraftproxy").
		Complete(r)
}
