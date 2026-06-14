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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServerType defines the Minecraft server software variant.
// +kubebuilder:validation:Enum=VANILLA;FORGE;FABRIC;PAPER;SPIGOT;BUKKIT;PURPUR;FOLIA
type ServerType string

const (
	ServerTypeVanilla ServerType = "VANILLA"
	ServerTypeForge   ServerType = "FORGE"
	ServerTypeFabric  ServerType = "FABRIC"
	ServerTypePaper   ServerType = "PAPER"
	ServerTypeSpigot  ServerType = "SPIGOT"
	ServerTypeBukkit  ServerType = "BUKKIT"
	ServerTypePurpur  ServerType = "PURPUR"
	ServerTypeFolia   ServerType = "FOLIA"
)

// Difficulty defines the in-game difficulty level.
// +kubebuilder:validation:Enum=peaceful;easy;normal;hard
type Difficulty string

// Gamemode defines the default game mode.
// +kubebuilder:validation:Enum=survival;creative;adventure;spectator
type Gamemode string

// MinecraftServerSpec defines the desired state of MinecraftServer.
type MinecraftServerSpec struct {
	// Version is the Minecraft version to run, e.g. "1.21.4" or "LATEST".
	// +kubebuilder:default="LATEST"
	Version string `json:"version,omitempty"`

	// Type is the server software to use.
	// +kubebuilder:default=VANILLA
	Type ServerType `json:"type,omitempty"`

	// MOTD is the message of the day shown in the server list.
	// +kubebuilder:default="A Minecraft Server"
	MOTD string `json:"motd,omitempty"`

	// MaxPlayers is the maximum number of players allowed.
	// +kubebuilder:default=20
	// +kubebuilder:validation:Minimum=1
	MaxPlayers int32 `json:"maxPlayers,omitempty"`

	// Difficulty sets the game difficulty.
	// +kubebuilder:default=easy
	Difficulty Difficulty `json:"difficulty,omitempty"`

	// Gamemode sets the default game mode for new players.
	// +kubebuilder:default=survival
	Gamemode Gamemode `json:"gamemode,omitempty"`

	// Memory configures the JVM heap size, e.g. "2G".
	// +kubebuilder:default="1G"
	Memory string `json:"memory,omitempty"`

	// Storage configures the PersistentVolumeClaim for /data.
	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`

	// ServiceType determines how the server pod is exposed internally.
	// Keep this ClusterIP when using a MinecraftProxy for external access.
	// +kubebuilder:default=ClusterIP
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// NodePort is the port used when ServiceType is NodePort.
	// +optional
	NodePort *int32 `json:"nodePort,omitempty"`

	// DisableProxy opts this server out of proxy-based routing entirely.
	// When true: no MinecraftProxy is created or updated, no domain is assigned,
	// and the server's Service is exposed directly via ServiceType.
	// Useful for standalone servers that need their own IP/port.
	// +kubebuilder:default=false
	DisableProxy bool `json:"disableProxy,omitempty"`

	// Domain is the public hostname players use to connect, e.g. "survival.mc.example.com".
	// Ignored when DisableProxy is true.
	// When unset and DisableProxy is false, a random subdomain is auto-assigned
	// using the proxy's BaseDomain.
	// +optional
	Domain *string `json:"domain,omitempty"`

	// ProxyRef is the name of the MinecraftProxy this server should register with.
	// If the proxy does not exist it is created automatically.
	// Ignored when DisableProxy is true. Defaults to "proxy".
	// +kubebuilder:default="proxy"
	ProxyRef string `json:"proxyRef,omitempty"`

	// Whitelist enables the server whitelist.
	// +kubebuilder:default=false
	Whitelist bool `json:"whitelist,omitempty"`

	// Ops is a comma-separated list of operator player names.
	// +optional
	Ops string `json:"ops,omitempty"`

	// Env allows passing arbitrary additional environment variables to the container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Resources sets compute resource requests/limits for the server container.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Image overrides the default itzg/minecraft-server image.
	// +optional
	Image *string `json:"image,omitempty"`
}

// StorageSpec defines PVC settings for the /data volume.
type StorageSpec struct {
	// Size is the requested storage capacity.
	// +kubebuilder:default="5Gi"
	Size resource.Quantity `json:"size,omitempty"`

	// StorageClassName is the name of the StorageClass to use.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`
}

// ServerPhase represents the lifecycle phase of a MinecraftServer.
type ServerPhase string

const (
	PhaseProvisioning ServerPhase = "Provisioning"
	PhaseRunning      ServerPhase = "Running"
	PhaseDegraded     ServerPhase = "Degraded"
)

// MinecraftServerStatus defines the observed state of MinecraftServer.
type MinecraftServerStatus struct {
	// Phase is the current lifecycle phase.
	Phase ServerPhase `json:"phase,omitempty"`

	// ReadyReplicas is the number of ready pods backing this server.
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// AssignedDomain is the effective hostname for this server.
	// Set from spec.domain if provided, otherwise auto-generated using the
	// proxy's baseDomain. Stable once set — never regenerated.
	AssignedDomain string `json:"assignedDomain,omitempty"`

	// Conditions holds standard condition objects.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Domain",type=string,JSONPath=`.status.assignedDomain`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// MinecraftServer is the Schema for the minecraftservers API.
type MinecraftServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MinecraftServerSpec   `json:"spec,omitempty"`
	Status MinecraftServerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MinecraftServerList contains a list of MinecraftServer.
type MinecraftServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MinecraftServer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MinecraftServer{}, &MinecraftServerList{})
}
