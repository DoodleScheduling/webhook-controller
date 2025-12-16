/*


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

package v1beta1

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ResponseType string

const (
	Async                    ResponseType = "Async"
	AwaitAllPreferSuccessful ResponseType = "AwaitAllPreferSuccessful"
	AwaitAllPreferFailed     ResponseType = "AwaitAllPreferFailed"
	AwaitAllReport           ResponseType = "AwaitAllReport"
)

// ReceiverSpec defines the desired state of Receiver
type ReceiverSpec struct {
	// Suspend reconciliation
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// Response type
	// +kubebuilder:default=Async
	ResponseType ResponseType `json:"responseType,omitempty"`

	// Body size limit
	BodySizeLimit int64 `json:"bodySizeLimit,omitempty"`

	// Timeout for the target requests
	// +kubebuilder:default="10s"
	Timeout metav1.Duration `json:"timeout,omitempty"`

	// Targets to forward (clone) requests to
	Targets []Target `json:"targets"`
}

type Target struct {
	// HTTP Path
	// +kubebuilder:default="/"
	Path string `json:"path,omitempty"`

	// Service name and port
	Service ServiceSelector `json:"service"`

	// NamespaceSelector defines a selector to select namespaces where services are looked up
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
}

type ServiceSelector struct {
	// Name of the service
	Name string `json:"name"`

	// Port of the service
	Port ServicePort `json:"port,omitempty"`
}

type ServicePort struct {
	// Name of the port, mutually exclusive with Number
	Name *string `json:"name,omitempty"`

	// Number of the port, mutually exclusive with Name
	Number *int32 `json:"number,omitempty"`
}

// ReceiverStatus defines the observed state of Receiver
type ReceiverStatus struct {
	// Conditions holds the conditions for the VaultBinding.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the last generation reconciled by the controller
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// The generated webhook path
	WebhookPath string `json:"webhookPath,omitempty"`

	// SubResourceCatalog holds discovered targets
	SubResourceCatalog []ResourceReference `json:"subResourceCatalog,omitempty"`
}

// ResourceReference metadata to lookup another resource
type ResourceReference struct {
	Kind       string `json:"kind,omitempty"`
	Name       string `json:"name,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
}

const (
	ReadyCondition            = "Ready"
	ServicePortNotFoundReason = "ServicePortNotFound"
	ServiceNotFoundReason     = "ServiceNotFound"
	ServiceBackendReadyReason = "ServiceBackendReady"
)

// ConditionalResource is a resource with conditions
type conditionalResource interface {
	GetStatusConditions() *[]metav1.Condition
}

// setResourceCondition sets the given condition with the given status,
// reason and message on a resource.
func setResourceCondition(resource conditionalResource, condition string, status metav1.ConditionStatus, reason, message string) {
	conditions := resource.GetStatusConditions()

	newCondition := metav1.Condition{
		Type:    condition,
		Status:  status,
		Reason:  reason,
		Message: message,
	}

	apimeta.SetStatusCondition(conditions, newCondition)
}

// ReceiverNotReady
func ReceiverNotReady(clone Receiver, reason, message string) Receiver {
	setResourceCondition(&clone, ReadyCondition, metav1.ConditionFalse, reason, message)
	return clone
}

// ReceiverReady
func ReceiverReady(clone Receiver, reason, message string) Receiver {
	setResourceCondition(&clone, ReadyCondition, metav1.ConditionTrue, reason, message)
	return clone
}

// GetStatusConditions returns a pointer to the Status.Conditions slice
func (in *Receiver) GetStatusConditions() *[]metav1.Condition {
	return &in.Status.Conditions
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=rc
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].message",description=""
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description=""

// Receiver is the Schema for the Receivers API
type Receiver struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReceiverSpec   `json:"spec,omitempty"`
	Status ReceiverStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReceiverList contains a list of Receiver
type ReceiverList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Receiver `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Receiver{}, &ReceiverList{})
}
