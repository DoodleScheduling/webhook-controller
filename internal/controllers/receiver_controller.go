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

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=webhook.infra.doodle.com,resources=receivers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=webhook.infra.doodle.com,resources=receivers/status,verbs=get;update;patch

package controllers

import (
	"cmp"
	"context"
	"fmt"
	"math/rand"
	"slices"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1beta1 "github.com/DoodleScheduling/webhook-controller/api/v1beta1"
	"github.com/DoodleScheduling/webhook-controller/internal/proxy"
)

// Receiver reconciles a Receiver object
type ReceiverReconciler struct {
	client.Client
	HttpProxy pathUpdater
	Log       logr.Logger
	Recorder  record.EventRecorder
}

type pathUpdater interface {
	RegisterOrUpdate(receiver proxy.Receiver) error
	Unregister(path string) error
}

type ReceiverReconcilerOptions struct {
	MaxConcurrentReconciles int
}

// SetupWithManager adding controllers
func (r *ReceiverReconciler) SetupWithManager(mgr ctrl.Manager, opts ReceiverReconcilerOptions) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Receiver{}).
		Watches(
			&v1.Service{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForChangeBySelector),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: opts.MaxConcurrentReconciles}).
		Complete(r)
}

func (r *ReceiverReconciler) requestsForChangeBySelector(ctx context.Context, o client.Object) []reconcile.Request {
	svc, ok := o.(*v1.Service)
	if !ok {
		panic(fmt.Sprintf("expected a Service, got %T", o))
	}

	var ns v1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: svc.Namespace}, &ns); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
	}

	var list v1beta1.ReceiverList
	if err := r.List(ctx, &list); err != nil {
		return nil
	}

	var reqs []reconcile.Request
	for _, receiver := range list.Items {
		receiver := receiver
		if receiver.Spec.Targets == nil {
			continue
		}

		for _, target := range receiver.Spec.Targets {
			if target.Service.Name != svc.Name {
				continue
			}

			if target.NamespaceSelector == nil {
				if receiver.Namespace == svc.Namespace {
					r.Log.V(1).Info("referenced resource from a Receiver changed detected", "namespace", receiver.Namespace, "receiver-name", receiver.Name)
					reqs = append(reqs, reconcile.Request{NamespacedName: objectKey(&receiver)})
				}
			} else {
				labelSel, err := metav1.LabelSelectorAsSelector(target.NamespaceSelector)
				if err != nil {
					r.Log.Error(err, "can not select resourceSelector selectors")
					continue
				}

				if labelSel.Matches(labels.Set(ns.GetLabels())) {
					r.Log.V(1).Info("referenced resource from a Receiver changed detected", "namespace", receiver.Namespace, "receiver-name", receiver.Name)
					reqs = append(reqs, reconcile.Request{NamespacedName: objectKey(&receiver)})
				}
			}
		}
	}

	return reqs
}

var chars = []rune("abcdefghijklmnopqrstuvwxyz123456789")

func randSeq(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// Reconcile Receivers
func (r *ReceiverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("Namespace", req.Namespace, "Name", req.NamespacedName)
	logger.Info("reconciling Receiver")

	// Fetch the Receiver instance
	receiver := v1beta1.Receiver{}

	err := r.Get(ctx, req.NamespacedName, &receiver)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if receiver.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	receiver.Status.ObservedGeneration = receiver.Generation
	receiver, result, reconcileErr := r.reconcile(ctx, receiver, logger)

	// Update status after reconciliation.
	if err = r.patchStatus(ctx, &receiver); err != nil {
		logger.Error(err, "unable to update status after reconciliation")
		return ctrl.Result{Requeue: true}, err
	}

	return result, reconcileErr
}

func (r *ReceiverReconciler) reconcile(ctx context.Context, receiver v1beta1.Receiver, logger logr.Logger) (v1beta1.Receiver, ctrl.Result, error) {
	receiver, services, err := r.extendWithTargets(ctx, receiver, logger)
	if err != nil {
		return receiver, ctrl.Result{}, err
	}

	if receiver.Status.WebhookPath == "" {
		receiver.Status.WebhookPath = fmt.Sprintf("/hooks/%s", randSeq(32))
	}

	var targets []proxy.Target

	for _, svc := range services {
		targets = append(targets, proxy.Target{
			Address:          svc.addr,
			ResponseType:     proxy.ResponseType(receiver.Spec.ResponseType),
			Port:             svc.port,
			ServiceName:      svc.ref.Name,
			ServiceNamespace: svc.ref.Namespace,
			Path:             svc.path,
		})
	}

	if len(targets) == 0 {
		if err := r.HttpProxy.Unregister(receiver.Status.WebhookPath); err != nil {
			return receiver, ctrl.Result{}, err
		}

		msg := "no targets found"
		r.Recorder.Event(&receiver, "Normal", "info", msg)
		return v1beta1.ReceiverNotReady(receiver, v1beta1.ServiceBackendReadyReason, msg), ctrl.Result{}, nil
	}

	err = r.HttpProxy.RegisterOrUpdate(proxy.Receiver{
		Timeout:       receiver.Spec.Timeout.Duration,
		Path:          receiver.Status.WebhookPath,
		Targets:       targets,
		ResponseType:  proxy.ResponseType(receiver.Spec.ResponseType),
		BodySizeLimit: receiver.Spec.BodySizeLimit,
	})

	if err != nil {
		return v1beta1.Receiver{}, ctrl.Result{}, err
	}

	msg := "receiver successfully registered"
	r.Recorder.Event(&receiver, "Normal", "info", msg)
	return v1beta1.ReceiverReady(receiver, v1beta1.ServiceBackendReadyReason, msg), ctrl.Result{}, err
}

type targetService struct {
	addr string
	port int32
	path string
	ref  v1beta1.ResourceReference
}

func (r *ReceiverReconciler) extendWithTargets(ctx context.Context, receiver v1beta1.Receiver, logger logr.Logger) (v1beta1.Receiver, []targetService, error) {
	var services []targetService

	receiver.Status.SubResourceCatalog = []v1beta1.ResourceReference{}

	for _, target := range receiver.Spec.Targets {
		var namespaces v1.NamespaceList
		if target.NamespaceSelector == nil {
			namespaces.Items = append(namespaces.Items, v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: receiver.Namespace,
				},
			})
		} else {
			namespaceSelector, err := metav1.LabelSelectorAsSelector(target.NamespaceSelector)
			if err != nil {
				return receiver, nil, err
			}

			err = r.List(ctx, &namespaces, client.MatchingLabelsSelector{Selector: namespaceSelector})
			if err != nil {
				return receiver, nil, err
			}
		}

		for _, namespace := range namespaces.Items {
			service := v1.Service{}
			err := r.Get(ctx, client.ObjectKey{
				Namespace: namespace.Name,
				Name:      target.Service.Name,
			}, &service)

			if err != nil {
				logger.V(1).Error(err, "no service found for target", "namespace", namespace.Name, "service", target.Service.Name)
				continue
			}

			var port int32
			for _, p := range service.Spec.Ports {
				if target.Service.Port.Name != nil && p.Name == *target.Service.Port.Name {
					port = p.Port
				} else if target.Service.Port.Number != nil && p.Port == *target.Service.Port.Number {
					port = p.Port
				}
			}

			if port == 0 {
				logger.V(1).Error(err, "port not found for target", "namespace", namespace.Name, "service", target.Service.Name)
				continue
			}

			if service.Spec.ClusterIP == "" {
				continue
			}

			services = append(services, targetService{
				addr: service.Spec.ClusterIP,
				path: target.Path,
				port: port,
				ref: v1beta1.ResourceReference{
					Kind:       service.Kind,
					Name:       service.Name,
					Namespace:  service.Namespace,
					APIVersion: service.APIVersion,
				},
			})
		}
	}

	slices.SortFunc(services, func(a, b targetService) int {
		return cmp.Or(
			cmp.Compare(a.ref.Name, b.ref.Name),
			cmp.Compare(a.ref.Namespace, b.ref.Namespace),
		)
	})

	for _, svc := range services {
		receiver.Status.SubResourceCatalog = append(receiver.Status.SubResourceCatalog, svc.ref)
	}

	return receiver, services, nil
}

func (r *ReceiverReconciler) patchStatus(ctx context.Context, receiver *v1beta1.Receiver) error {
	key := client.ObjectKeyFromObject(receiver)
	latest := &v1beta1.Receiver{}
	if err := r.Get(ctx, key, latest); err != nil {
		return err
	}

	return r.Status().Patch(ctx, receiver, client.MergeFrom(latest))
}

// objectKey returns client.ObjectKey for the object.
func objectKey(object metav1.Object) client.ObjectKey {
	return client.ObjectKey{
		Namespace: object.GetNamespace(),
		Name:      object.GetName(),
	}
}
