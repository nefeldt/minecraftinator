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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RouteEntry maps a hostname to a backend MinecraftServer.
type RouteEntry struct {
	// Host is the hostname players connect to, e.g. "survival.mc.example.com".
	// +kubebuilder:validation:MinLength=1
	Host string `json:"host"`

	// ServerRef is the name of the MinecraftServer in the same namespace.
	// +kubebuilder:validation:MinLength=1
	ServerRef string `json:"serverRef"`
}

// MinecraftProxySpec defines the desired state of MinecraftProxy.
type MinecraftProxySpec struct {
	// BaseDomain is used to auto-assign subdomains to MinecraftServer instances
	// that have no explicit spec.domain set.
	// A random 6-character ID is generated and prepended: "<id>.<baseDomain>".
	// Example: "mc.feldt.systems" → "x7k2mq.mc.feldt.systems"
	// +optional
	BaseDomain string `json:"baseDomain,omitempty"`

	// Routes allows manually adding hostname→server mappings that are not
	// managed by a MinecraftServer resource (e.g. external servers).
	// Auto-discovered routes from MinecraftServer.status.assignedDomain take
	// precedence over entries here with the same host.
	// +optional
	Routes []RouteEntry `json:"routes,omitempty"`

	// ServiceType determines how mc-router itself is exposed.
	// +kubebuilder:default=LoadBalancer
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// Image overrides the default itzg/mc-router image.
	// +optional
	Image *string `json:"image,omitempty"`
}

// ActiveRoute is a resolved route shown in status.
type ActiveRoute struct {
	// Host is the public hostname.
	Host string `json:"host"`
	// ServerRef is the MinecraftServer name backing this route.
	ServerRef string `json:"serverRef"`
	// Backend is the resolved cluster-internal address.
	Backend string `json:"backend"`
	// Source indicates whether this route was auto-discovered or manually specified.
	// +kubebuilder:validation:Enum=auto;manual
	Source string `json:"source"`
}

// MinecraftProxyStatus defines the observed state of MinecraftProxy.
type MinecraftProxyStatus struct {
	// Phase is the current lifecycle phase.
	Phase string `json:"phase,omitempty"`

	// ActiveRoutes lists all currently active hostname→backend mappings.
	// +optional
	ActiveRoutes []ActiveRoute `json:"activeRoutes,omitempty"`

	// Conditions holds standard condition objects.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Routes",type=integer,JSONPath=`.status.activeRoutes`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MinecraftProxy is the Schema for the minecraftproxies API.
// It deploys an itzg/mc-router instance that routes multiple Minecraft servers
// behind a single IP and port (25565) based on the hostname the client connects to.
// Routes are auto-discovered from MinecraftServer resources that set spec.domain.
type MinecraftProxy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MinecraftProxySpec   `json:"spec,omitempty"`
	Status MinecraftProxyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MinecraftProxyList contains a list of MinecraftProxy.
type MinecraftProxyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MinecraftProxy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MinecraftProxy{}, &MinecraftProxyList{})
}
