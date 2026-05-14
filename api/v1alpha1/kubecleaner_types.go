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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KubeCleanerSpec defines the desired state of KubeCleaner.
type KubeCleanerSpec struct {
	// Interval defines how often the cleanup runs.
	// Must be a valid Go duration string (e.g., "5m", "1h", "30s").
	// Supported units: ns, us, ms, s, m, h.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=2
	// +kubebuilder:validation:Pattern=`^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`
	Interval string `json:"interval"`

	// DryRun when true causes the controller to only log what would be
	// deleted without actually deleting any resources.
	// +kubebuilder:default=false
	// +optional
	DryRun bool `json:"dryRun,omitempty"`

	// ExcludeLabels is a map of label key-value pairs. Resources with ANY
	// of these labels (key AND value match) are excluded from cleanup.
	// +optional
	ExcludeLabels map[string]string `json:"excludeLabels,omitempty"`

	// Selector is an optional label selector. If provided, only resources
	// matching this selector are targeted for cleanup. If empty or not set,
	// ALL resources (except those excluded) are targeted.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
}

// KubeCleanerStatus defines the observed state of KubeCleaner.
type KubeCleanerStatus struct {
	// LastCleanupTime is the timestamp of the last cleanup run.
	// +optional
	LastCleanupTime *metav1.Time `json:"lastCleanupTime,omitempty"`

	// DeletedCount is the total number of resources deleted (or would-be-deleted in dry-run) in the last run.
	// +optional
	DeletedCount int32 `json:"deletedCount,omitempty"`

	// Message is a human-readable message indicating details about the last cleanup run.
	// +optional
	Message string `json:"message,omitempty"`

	// Conditions represent the latest available observations of the KubeCleaner's state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Interval",type=string,JSONPath=`.spec.interval`
// +kubebuilder:printcolumn:name="DryRun",type=boolean,JSONPath=`.spec.dryRun`
// +kubebuilder:printcolumn:name="Last Cleanup",type=date,JSONPath=`.status.lastCleanupTime`
// +kubebuilder:printcolumn:name="Deleted",type=integer,JSONPath=`.status.deletedCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KubeCleaner is the Schema for the kubecleaners API.
type KubeCleaner struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec KubeCleanerSpec `json:"spec"`

	// +optional
	Status KubeCleanerStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// KubeCleanerList contains a list of KubeCleaner.
type KubeCleanerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KubeCleaner `json:"items"`
}

func init() {
	SchemeBuilder.Register(&KubeCleaner{}, &KubeCleanerList{})
}
