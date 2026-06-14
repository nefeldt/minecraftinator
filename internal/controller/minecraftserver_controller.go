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
	"crypto/rand"
	"encoding/hex"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	minecraftv1alpha1 "github.com/mittwald/minecraftinator/api/v1alpha1"
)

const (
	defaultImage   = "itzg/minecraft-server:latest"
	dataVolumeName = "data"
	dataMountPath  = "/data"
	minecraftPort  = 25565
	finalizerName  = "minecraft.mittwald.de/finalizer"
)

// MinecraftServerReconciler reconciles a MinecraftServer object
type MinecraftServerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=minecraft.mittwald.de,resources=minecraftservers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=minecraft.mittwald.de,resources=minecraftservers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=minecraft.mittwald.de,resources=minecraftservers/finalizers,verbs=update
// +kubebuilder:rbac:groups=minecraft.mittwald.de,resources=minecraftproxies,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete

func (r *MinecraftServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	mc := &minecraftv1alpha1.MinecraftServer{}
	if err := r.Get(ctx, req.NamespacedName, mc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !mc.DeletionTimestamp.IsZero() {
		controllerutil.RemoveFinalizer(mc, finalizerName)
		return ctrl.Result{}, r.Update(ctx, mc)
	}

	if !controllerutil.ContainsFinalizer(mc, finalizerName) {
		controllerutil.AddFinalizer(mc, finalizerName)
		if err := r.Update(ctx, mc); err != nil {
			return ctrl.Result{}, err
		}
	}

	if err := r.reconcilePVC(ctx, mc); err != nil {
		log.Error(err, "failed to reconcile PVC")
		return ctrl.Result{}, err
	}

	if err := r.reconcileDeployment(ctx, mc); err != nil {
		log.Error(err, "failed to reconcile Deployment")
		return ctrl.Result{}, err
	}

	if err := r.reconcileService(ctx, mc); err != nil {
		log.Error(err, "failed to reconcile Service")
		return ctrl.Result{}, err
	}

	if !mc.Spec.DisableProxy {
		proxy, err := r.ensureProxy(ctx, mc)
		if err != nil {
			log.Error(err, "failed to ensure MinecraftProxy")
			return ctrl.Result{}, err
		}
		if err := r.ensureAssignedDomain(ctx, mc, proxy); err != nil {
			log.Error(err, "failed to assign domain")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, r.updateStatus(ctx, mc)
}

// ensureProxy creates the MinecraftProxy named by spec.proxyRef if it doesn't exist,
// and returns it. If the proxy exists but has no BaseDomain and the server provides one,
// it patches the proxy so auto-generated subdomains work without manual proxy setup.
func (r *MinecraftServerReconciler) ensureProxy(ctx context.Context, mc *minecraftv1alpha1.MinecraftServer) (*minecraftv1alpha1.MinecraftProxy, error) {
	proxyName := mc.Spec.ProxyRef
	if proxyName == "" {
		proxyName = defaultProxyName
	}

	proxy := &minecraftv1alpha1.MinecraftProxy{}
	err := r.Get(ctx, types.NamespacedName{Name: proxyName, Namespace: mc.Namespace}, proxy)
	if errors.IsNotFound(err) {
		desired := &minecraftv1alpha1.MinecraftProxy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      proxyName,
				Namespace: mc.Namespace,
			},
			Spec: minecraftv1alpha1.MinecraftProxySpec{
				BaseDomain:  mc.Spec.BaseDomain,
				ServiceType: corev1.ServiceTypeLoadBalancer,
			},
		}
		if createErr := r.Create(ctx, desired); createErr != nil {
			return nil, createErr
		}
		return desired, nil
	}
	if err != nil {
		return nil, err
	}

	// If the proxy has no BaseDomain yet and this server provides one, set it.
	if proxy.Spec.BaseDomain == "" && mc.Spec.BaseDomain != "" {
		proxy.Spec.BaseDomain = mc.Spec.BaseDomain
		if updateErr := r.Update(ctx, proxy); updateErr != nil {
			return nil, updateErr
		}
	}

	return proxy, nil
}

// ensureAssignedDomain sets status.assignedDomain if it hasn't been set yet.
// Priority: spec.domain > auto-generated using proxy.spec.baseDomain.
// Once set, the value is never changed so players' DNS entries stay stable.
func (r *MinecraftServerReconciler) ensureAssignedDomain(ctx context.Context, mc *minecraftv1alpha1.MinecraftServer, proxy *minecraftv1alpha1.MinecraftProxy) error {
	if mc.Status.AssignedDomain != "" {
		return nil // already assigned, nothing to do
	}

	var domain string
	if mc.Spec.Domain != nil {
		domain = *mc.Spec.Domain
	} else if proxy.Spec.BaseDomain != "" {
		id, err := randomID(6)
		if err != nil {
			return fmt.Errorf("generating random id: %w", err)
		}
		domain = fmt.Sprintf("%s.%s", id, proxy.Spec.BaseDomain)
	} else {
		return nil // no domain and no baseDomain configured — skip
	}

	mc.Status.AssignedDomain = domain
	return r.Status().Update(ctx, mc)
}

func randomID(n int) (string, error) {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b)[:n], nil
}

func (r *MinecraftServerReconciler) reconcilePVC(ctx context.Context, mc *minecraftv1alpha1.MinecraftServer) error {
	pvc := &corev1.PersistentVolumeClaim{}
	name := types.NamespacedName{Name: mc.Name, Namespace: mc.Namespace}

	err := r.Get(ctx, name, pvc)
	if err == nil {
		return nil // PVC already exists; don't resize
	}
	if !errors.IsNotFound(err) {
		return err
	}

	size := resource.MustParse("5Gi")
	if mc.Spec.Storage != nil && !mc.Spec.Storage.Size.IsZero() {
		size = mc.Spec.Storage.Size
	}

	desired := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mc.Name,
			Namespace: mc.Namespace,
			Labels:    labelsFor(mc),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: size,
				},
			},
		},
	}
	if mc.Spec.Storage != nil {
		desired.Spec.StorageClassName = mc.Spec.Storage.StorageClassName
	}

	if err := controllerutil.SetControllerReference(mc, desired, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, desired)
}

func (r *MinecraftServerReconciler) reconcileDeployment(ctx context.Context, mc *minecraftv1alpha1.MinecraftServer) error {
	desired := r.deploymentFor(mc)
	if err := controllerutil.SetControllerReference(mc, desired, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: mc.Name, Namespace: mc.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Spec.Template = desired.Spec.Template
	existing.Spec.Replicas = desired.Spec.Replicas
	existing.Labels = desired.Labels
	return r.Update(ctx, existing)
}

func (r *MinecraftServerReconciler) reconcileService(ctx context.Context, mc *minecraftv1alpha1.MinecraftServer) error {
	desired := r.serviceFor(mc)
	if err := controllerutil.SetControllerReference(mc, desired, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: mc.Name, Namespace: mc.Namespace}, existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Spec.Type = desired.Spec.Type
	existing.Spec.Ports = desired.Spec.Ports
	existing.Labels = desired.Labels
	return r.Update(ctx, existing)
}

func (r *MinecraftServerReconciler) updateStatus(ctx context.Context, mc *minecraftv1alpha1.MinecraftServer) error {
	dep := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: mc.Name, Namespace: mc.Namespace}, dep); err != nil {
		return client.IgnoreNotFound(err)
	}

	mc.Status.ReadyReplicas = dep.Status.ReadyReplicas
	if dep.Status.ReadyReplicas > 0 {
		mc.Status.Phase = minecraftv1alpha1.PhaseRunning
	} else {
		mc.Status.Phase = minecraftv1alpha1.PhaseProvisioning
	}

	return r.Status().Update(ctx, mc)
}

func (r *MinecraftServerReconciler) deploymentFor(mc *minecraftv1alpha1.MinecraftServer) *appsv1.Deployment {
	labels := labelsFor(mc)
	replicas := int32(1)
	image := defaultImage
	if mc.Spec.Image != nil {
		image = *mc.Spec.Image
	}

	resources := corev1.ResourceRequirements{}
	if mc.Spec.Resources != nil {
		resources = *mc.Spec.Resources
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mc.Name,
			Namespace: mc.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Strategy: appsv1.DeploymentStrategy{
				// Minecraft servers are stateful; recreate avoids split-brain.
				Type: appsv1.RecreateDeploymentStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "minecraft",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env:             r.buildEnv(mc),
							Ports: []corev1.ContainerPort{
								{Name: "minecraft", ContainerPort: minecraftPort, Protocol: corev1.ProtocolTCP},
							},
							Resources: resources,
							VolumeMounts: []corev1.VolumeMount{
								{Name: dataVolumeName, MountPath: dataMountPath},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt32(minecraftPort),
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt32(minecraftPort),
									},
								},
								InitialDelaySeconds: 60,
								PeriodSeconds:       20,
								FailureThreshold:    3,
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: dataVolumeName,
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: mc.Name,
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *MinecraftServerReconciler) serviceFor(mc *minecraftv1alpha1.MinecraftServer) *corev1.Service {
	labels := labelsFor(mc)
	svcType := corev1.ServiceTypeClusterIP
	if mc.Spec.ServiceType != "" {
		svcType = mc.Spec.ServiceType
	}

	port := corev1.ServicePort{
		Name:       "minecraft",
		Port:       minecraftPort,
		TargetPort: intstr.FromInt32(minecraftPort),
		Protocol:   corev1.ProtocolTCP,
	}
	if svcType == corev1.ServiceTypeNodePort && mc.Spec.NodePort != nil {
		port.NodePort = *mc.Spec.NodePort
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mc.Name,
			Namespace: mc.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     svcType,
			Selector: labels,
			Ports:    []corev1.ServicePort{port},
		},
	}
}

func (r *MinecraftServerReconciler) buildEnv(mc *minecraftv1alpha1.MinecraftServer) []corev1.EnvVar {
	serverType := minecraftv1alpha1.ServerTypeVanilla
	if mc.Spec.Type != "" {
		serverType = mc.Spec.Type
	}
	version := "LATEST"
	if mc.Spec.Version != "" {
		version = mc.Spec.Version
	}
	memory := "1G"
	if mc.Spec.Memory != "" {
		memory = mc.Spec.Memory
	}
	motd := "A Minecraft Server"
	if mc.Spec.MOTD != "" {
		motd = mc.Spec.MOTD
	}
	maxPlayers := int32(20)
	if mc.Spec.MaxPlayers > 0 {
		maxPlayers = mc.Spec.MaxPlayers
	}
	difficulty := minecraftv1alpha1.Difficulty("easy")
	if mc.Spec.Difficulty != "" {
		difficulty = mc.Spec.Difficulty
	}
	gamemode := minecraftv1alpha1.Gamemode("survival")
	if mc.Spec.Gamemode != "" {
		gamemode = mc.Spec.Gamemode
	}

	env := []corev1.EnvVar{
		{Name: "EULA", Value: "TRUE"},
		{Name: "TYPE", Value: string(serverType)},
		{Name: "VERSION", Value: version},
		{Name: "MEMORY", Value: memory},
		{Name: "MOTD", Value: motd},
		{Name: "MAX_PLAYERS", Value: fmt.Sprintf("%d", maxPlayers)},
		{Name: "DIFFICULTY", Value: string(difficulty)},
		{Name: "MODE", Value: string(gamemode)},
		{Name: "ENABLE_WHITELIST", Value: boolToStr(mc.Spec.Whitelist)},
	}
	if mc.Spec.Ops != "" {
		env = append(env, corev1.EnvVar{Name: "OPS", Value: mc.Spec.Ops})
	}

	// User-supplied extra env vars override defaults (last wins in itzg image).
	env = append(env, mc.Spec.Env...)
	return env
}

func labelsFor(mc *minecraftv1alpha1.MinecraftServer) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "minecraft-server",
		"app.kubernetes.io/instance":   mc.Name,
		"app.kubernetes.io/managed-by": "minecraftinator",
	}
}

func boolToStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// SetupWithManager sets up the controller with the Manager.
func (r *MinecraftServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&minecraftv1alpha1.MinecraftServer{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Named("minecraftserver").
		Complete(r)
}
