/*
Copyright 2022 The Flux authors

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

package v1beta2

import (
	"github.com/fluxcd/pkg/apis/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AlertKind string = "Alert"
)

// AlertSpec defines an alerting rule for events involving a list of objects.
type AlertSpec struct {
	// ProviderRef specifies which Provider this Alert should use.
	// +required
	ProviderRef meta.LocalObjectReference `json:"providerRef"`

	// EventSeverity specifies how to filter events based on severity.
	// If set to 'info' no events will be filtered.
	// +kubebuilder:validation:Enum=info;error
	// +kubebuilder:default:=info
	// +optional
	EventSeverity string `json:"eventSeverity,omitempty"`

	// EventSources specifies how to filter events based
	// on the involved object kind, name and namespace.
	// +required
	EventSources []CrossNamespaceObjectReference `json:"eventSources"`

	// ExclusionList specifies a list of Golang regular expressions
	// to be used for excluding messages.
	// +optional
	ExclusionList []string `json:"exclusionList,omitempty"`

	// Summary holds a short description of the impact and affected cluster.
	// +kubebuilder:validation:MaxLength:=255
	// +optional
	Summary string `json:"summary,omitempty"`

	// Suspend tells the controller to suspend subsequent
	// events handling for this Alert.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// +genclient
// +genclient:Namespaced
// +kubebuilder:storageversion
// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description=""

// Alert is the Schema for the alerts API
type Alert struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec AlertSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// AlertList contains a list of Alerts.
type AlertList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Alert `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Alert{}, &AlertList{})
}
