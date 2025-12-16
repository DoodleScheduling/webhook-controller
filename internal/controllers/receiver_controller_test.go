package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/DoodleScheduling/webhook-controller/api/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var _ = Describe("Receiver controller", func() {
	const (
		timeout  = time.Second * 4
		interval = time.Millisecond * 200
	)

	var eventuallyMatchExactConditions = func(ctx context.Context, instanceLookupKey types.NamespacedName, reconciledInstance *v1beta1.Receiver, expectedStatus *v1beta1.ReceiverStatus) {
		Eventually(func() error {
			err := k8sClient.Get(ctx, instanceLookupKey, reconciledInstance)
			if err != nil {
				return err
			}

			return needsExactConditions(expectedStatus.Conditions, reconciledInstance.Status.Conditions)
		}, timeout, interval).Should(BeNil())
	}

	When("reconciling a suspended Receiver", func() {
		receiverName := fmt.Sprintf("receiver-%s", randStringRunes(5))

		It("should not update the status", func() {
			By("creating a new Receiver")
			ctx := context.Background()

			receiver := &v1beta1.Receiver{
				ObjectMeta: metav1.ObjectMeta{
					Name:      receiverName,
					Namespace: "default",
				},
				Spec: v1beta1.ReceiverSpec{
					Suspend: true,
					Targets: []v1beta1.Target{},
				},
			}
			Expect(k8sClient.Create(ctx, receiver)).Should(Succeed())

			By("waiting for the reconciliation")
			instanceLookupKey := types.NamespacedName{Name: receiverName, Namespace: "default"}
			reconciledInstance := &v1beta1.Receiver{}

			eventuallyMatchExactConditions(ctx, instanceLookupKey, reconciledInstance, &v1beta1.ReceiverStatus{})
		})
	})

	When("it reconciles a receiver without targets", func() {
		receiverName := fmt.Sprintf("receiver-%s", randStringRunes(5))
		var receiver *v1beta1.Receiver

		It("creates a new receiver", func() {
			ctx := context.Background()

			receiver = &v1beta1.Receiver{
				ObjectMeta: metav1.ObjectMeta{
					Name:      receiverName,
					Namespace: "default",
				},
				Spec: v1beta1.ReceiverSpec{
					Targets: []v1beta1.Target{},
				},
			}
			Expect(k8sClient.Create(ctx, receiver)).Should(Succeed())
		})

		It("should update the receiver status", func() {
			ctx := context.Background()
			reconciledInstance := &v1beta1.Receiver{}
			instanceLookupKey := types.NamespacedName{Name: receiverName, Namespace: "default"}

			expectedStatus := &v1beta1.ReceiverStatus{
				ObservedGeneration: 1,
				Conditions: []metav1.Condition{
					{
						Type:    v1beta1.ReadyCondition,
						Status:  metav1.ConditionFalse,
						Reason:  "ServiceBackendReady",
						Message: "no targets found",
					},
				},
			}
			eventuallyMatchExactConditions(ctx, instanceLookupKey, reconciledInstance, expectedStatus)
		})

		It("cleans up", func() {
			ctx := context.Background()
			Expect(k8sClient.Delete(ctx, receiver)).Should(Succeed())
		})
	})

	When("it reconciles a Receiver with targets", func() {
		svcName := fmt.Sprintf("svc-%s", randStringRunes(5))
		receiverName := fmt.Sprintf("receiver-%s", randStringRunes(5))
		var receiver *v1beta1.Receiver
		portName := "http"
		var svc *v1.Service

		It("creates a new Receiver", func() {
			ctx := context.Background()

			receiver = &v1beta1.Receiver{
				ObjectMeta: metav1.ObjectMeta{
					Name:      receiverName,
					Namespace: "default",
				},
				Spec: v1beta1.ReceiverSpec{
					Targets: []v1beta1.Target{
						{
							Service: v1beta1.ServiceSelector{
								Name: svcName,
								Port: v1beta1.ServicePort{
									Name: &portName,
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, receiver)).Should(Succeed())
		})

		It("should update the Receiver status", func() {
			ctx := context.Background()
			reconciledInstance := &v1beta1.Receiver{}
			instanceLookupKey := types.NamespacedName{Name: receiverName, Namespace: "default"}

			expectedStatus := &v1beta1.ReceiverStatus{
				ObservedGeneration: 1,
				Conditions: []metav1.Condition{
					{
						Type:    v1beta1.ReadyCondition,
						Status:  metav1.ConditionFalse,
						Reason:  "ServiceBackendReady",
						Message: "no targets found",
					},
				},
			}
			eventuallyMatchExactConditions(ctx, instanceLookupKey, reconciledInstance, expectedStatus)
		})

		It("creates service for target", func() {
			ctx := context.Background()
			svc = &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      svcName,
					Namespace: "default",
				},
				Spec: v1.ServiceSpec{
					Ports: []v1.ServicePort{
						{
							Name:       portName,
							Port:       8080,
							TargetPort: intstr.FromInt(8080),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, svc)).Should(Succeed())
		})

		It("should update the Receiver status", func() {
			ctx := context.Background()
			reconciledInstance := &v1beta1.Receiver{}
			instanceLookupKey := types.NamespacedName{Name: receiverName, Namespace: "default"}

			expectedStatus := &v1beta1.ReceiverStatus{
				ObservedGeneration: 1,
				Conditions: []metav1.Condition{
					{
						Type:    v1beta1.ReadyCondition,
						Status:  metav1.ConditionTrue,
						Reason:  "ServiceBackendReady",
						Message: "receiver successfully registered",
					},
				},
			}
			eventuallyMatchExactConditions(ctx, instanceLookupKey, reconciledInstance, expectedStatus)
		})

		It("cleans up", func() {
			ctx := context.Background()
			Expect(k8sClient.Delete(ctx, svc)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, receiver)).Should(Succeed())
		})
	})
})
