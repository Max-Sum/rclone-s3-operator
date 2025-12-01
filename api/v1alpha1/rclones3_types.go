package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BucketSpec defines the desired state of an rclone served S3 gateway.
type BucketSpec struct {
	// Remote is the name of the rclone remote that should be exposed as S3.
	// +kubebuilder:validation:MinLength=1
	Remote string `json:"remote"`

	// Port the S3 gateway should listen on. Defaults to 9000.
	Port int32 `json:"port,omitempty"`

	// ServiceType allows switching between ClusterIP/NodePort/LoadBalancer.
	// Defaults to ClusterIP.
	ServiceType corev1.ServiceType `json:"serviceType,omitempty"`

	// ServiceName overrides the generated Service/Deployment prefix.
	ServiceName string `json:"serviceName,omitempty"`

	// SecretName overrides the generated credential Secret name in the CR namespace.
	SecretName string `json:"secretName,omitempty"`

	// Image is the rclone container image to run. Defaults to rclone/rclone:latest.
	Image string `json:"image,omitempty"`

	// Replicas controls the number of serve pods. Defaults to 1.
	Replicas *int32 `json:"replicas,omitempty"`

	// ReadOnly switches rclone into read-only mode.
	ReadOnly bool `json:"readOnly,omitempty"`

	// PodLabels merges additional labels onto the pod template.
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// PodAnnotations merges additional annotations onto the pod template.
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// PodAffinity configures pod affinity for the rclone pod.
	PodAffinity *corev1.PodAffinity `json:"podAffinity,omitempty"`

	// NodeAffinity configures node affinity for the rclone pod.
	NodeAffinity *corev1.NodeAffinity `json:"nodeAffinity,omitempty"`

	// ExtraArgs allow passing additional flags to rclone serve s3 verbatim.
	ExtraArgs []string `json:"extraArgs,omitempty"`

	// Resources sets container resource requests/limits.
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// BucketStatus captures the observed state of a gateway.
type BucketStatus struct {
	// Ready reflects whether the desired resources have been created.
	Ready bool `json:"ready,omitempty"`

	// Endpoint is the cluster DNS endpoint for the gateway.
	Endpoint string `json:"endpoint,omitempty"`

	// SecretName is the name of the generated credential Secret.
	SecretName string `json:"secretName,omitempty"`

	// ObservedGeneration mirrors metadata.generation once reconciled.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions provide granular reconciliation states.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Bucket exposes an rclone remote via S3 compatible API.
type Bucket struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BucketSpec   `json:"spec,omitempty"`
	Status BucketStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// BucketList contains a list of Bucket objects.
type BucketList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Bucket `json:"items"`
}
