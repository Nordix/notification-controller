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
	"crypto/sha256"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/fluxcd/pkg/apis/meta"
)

const (
	ReceiverKind        string = "Receiver"
	ReceiverWebhookPath string = "/hook/"
	GenericReceiver     string = "generic"
	GenericHMACReceiver string = "generic-hmac"
	GitHubReceiver      string = "github"
	GitLabReceiver      string = "gitlab"
	BitbucketReceiver   string = "bitbucket"
	HarborReceiver      string = "harbor"
	DockerHubReceiver   string = "dockerhub"
	QuayReceiver        string = "quay"
	GCRReceiver         string = "gcr"
	NexusReceiver       string = "nexus"
	ACRReceiver         string = "acr"
)

// ReceiverSpec defines the desired state of the Receiver.
type ReceiverSpec struct {
	// Type of webhook sender, used to determine
	// the validation procedure and payload deserialization.
	// +kubebuilder:validation:Enum=generic;generic-hmac;github;gitlab;bitbucket;harbor;dockerhub;quay;gcr;nexus;acr
	// +required
	Type string `json:"type"`

	// Interval at which to reconcile the Receiver with its Secret references.
	// +kubebuilder:default="10m"
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$"
	// +optional
	Interval metav1.Duration `json:"interval"`

	// Events specifies the list of event types to handle,
	// e.g. 'push' for GitHub or 'Push Hook' for GitLab.
	// +optional
	Events []string `json:"events"`

	// A list of resources to be notified about changes.
	// +required
	Resources []CrossNamespaceObjectReference `json:"resources"`

	// SecretRef specifies the Secret containing the token used
	// to validate the payload authenticity.
	// +required
	SecretRef meta.LocalObjectReference `json:"secretRef,omitempty"`

	// Suspend tells the controller to suspend subsequent
	// events handling for this receiver.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// ReceiverStatus defines the observed state of the Receiver.
type ReceiverStatus struct {
	meta.ReconcileRequestStatus `json:",inline"`

	// Conditions holds the conditions for the Receiver.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// URL is the generated incoming webhook address in the format
	// of '/hook/sha256sum(token+name+namespace)'.
	// Deprecated: Replaced by WebhookPath.
	// +optional
	URL string `json:"url,omitempty"`

	// WebhookPath is the generated incoming webhook address in the format
	// of '/hook/sha256sum(token+name+namespace)'.
	// +optional
	WebhookPath string `json:"webhookPath,omitempty"`

	// ObservedGeneration is the last observed generation of the Receiver object.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// GetConditions returns the status conditions of the object.
func (in *Receiver) GetConditions() []metav1.Condition {
	return in.Status.Conditions
}

// SetConditions sets the status conditions on the object.
func (in *Receiver) SetConditions(conditions []metav1.Condition) {
	in.Status.Conditions = conditions
}

// GetWebhookPath returns the incoming webhook path for the given token.
func (in *Receiver) GetWebhookPath(token string) string {
	digest := sha256.Sum256([]byte(token + in.GetName() + in.GetNamespace()))
	return fmt.Sprintf("%s%x", ReceiverWebhookPath, digest)
}

// +genclient
// +genclient:Namespaced
// +kubebuilder:storageversion
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description=""
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].message",description=""

// Receiver is the Schema for the receivers API.
type Receiver struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ReceiverSpec `json:"spec,omitempty"`
	// +kubebuilder:default:={"observedGeneration":-1}
	Status ReceiverStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReceiverList contains a list of Receivers.
type ReceiverList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Receiver `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Receiver{}, &ReceiverList{})
}
