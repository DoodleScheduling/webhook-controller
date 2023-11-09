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

// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=proxy.infra.doodle.com,resources=requestclones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=proxy.infra.doodle.com,resources=requestclones/status,verbs=get;update;patch

package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1beta1 "github.com/DoodleScheduling/webhook-controller/api/v1beta1"
	"github.com/DoodleScheduling/webhook-controller/internal/proxy"
)

const (
	serviceIndex = ".metadata.service"
)

// RequestClone reconciles a RequestClone object
type RequestCloneReconciler struct {
	client.Client
	HttpProxy *proxy.HttpProxy
	Log       logr.Logger
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
}

type RequestCloneReconcilerOptions struct {
	MaxConcurrentReconciles int
}

// SetupWithManager adding controllers
func (r *RequestCloneReconciler) SetupWithManager(mgr ctrl.Manager, opts RequestCloneReconcilerOptions) error {
	// Index the ReqeustClones by the Service references they point at
	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &v1beta1.RequestClone{}, serviceIndex,
		func(o client.Object) []string {
			vb := o.(*v1beta1.RequestClone)
			r.Log.Info(fmt.Sprintf("%s/%s", vb.GetNamespace(), vb.Spec.Backend.ServiceName))
			return []string{
				fmt.Sprintf("%s/%s", vb.GetNamespace(), vb.Spec.Backend.ServiceName),
			}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.RequestClone{}).
		Watches(
			&v1.Service{},
			handler.EnqueueRequestsFromMapFunc(r.requestsForServiceChange),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: opts.MaxConcurrentReconciles}).
		Complete(r)
}

func (r *RequestCloneReconciler) requestsForServiceChange(ctx context.Context, o client.Object) []reconcile.Request {
	s, ok := o.(*v1.Service)
	if !ok {
		panic(fmt.Sprintf("expected a Service, got %T", o))
	}

	var list v1beta1.RequestCloneList
	if err := r.List(ctx, &list, client.MatchingFields{
		serviceIndex: objectKey(s).String(),
	}); err != nil {
		return nil
	}

	var reqs []reconcile.Request
	for _, i := range list.Items {
		r.Log.Info("referenced service from a requestclone changed detected, reconcile requestclone", "namespace", i.GetNamespace(), "name", i.GetName())
		reqs = append(reqs, reconcile.Request{NamespacedName: objectKey(&i)})
	}

	return reqs
}

// Reconcile RequestClones
func (r *RequestCloneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("Namespace", req.Namespace, "Name", req.NamespacedName)
	logger.Info("reconciling RequestClone")

	// Fetch the RequestClone instance
	ph := v1beta1.RequestClone{}

	err := r.Client.Get(ctx, req.NamespacedName, &ph)
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

	ph, result, reconcileErr := r.reconcile(ctx, ph, logger)

	// Update status after reconciliation.
	if err = r.patchStatus(ctx, &ph); err != nil {
		logger.Error(err, "unable to update status after reconciliation")
		return ctrl.Result{Requeue: true}, err
	}

	return result, reconcileErr
}

func (r *RequestCloneReconciler) reconcile(ctx context.Context, ph v1beta1.RequestClone, logger logr.Logger) (v1beta1.RequestClone, ctrl.Result, error) {
	// Lookup matching service
	svc := v1.Service{}
	err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: ph.GetNamespace(),
		Name:      ph.Spec.Backend.ServiceName,
	}, &svc)

	if err != nil {
		msg := "Service not found"
		r.Recorder.Event(&ph, "Normal", "info", msg)
		return v1beta1.RequestCloneNotReady(ph, v1beta1.ServiceNotFoundReason, msg), ctrl.Result{}, nil
	}

	var port int32
	for _, p := range svc.Spec.Ports {
		if p.Name == ph.Spec.Backend.ServicePort {
			port = p.Port
		}
	}

	if port == 0 {
		msg := "Port not found in service"
		r.Recorder.Event(&ph, "Normal", "info", msg)
		return v1beta1.RequestCloneNotReady(ph, v1beta1.ServicePortNotFoundReason, msg), ctrl.Result{}, nil
	}

	_ = r.HttpProxy.RegisterOrUpdate(proxy.RequestClone{
		Host:    ph.Spec.Host,
		Service: svc.Spec.ClusterIP,
		Port:    port,
		Object: client.ObjectKey{
			Namespace: ph.GetNamespace(),
			Name:      ph.GetName(),
		},
	})

	msg := "Service backend successfully registered"
	r.Recorder.Event(&ph, "Normal", "info", msg)
	return v1beta1.RequestCloneReady(ph, v1beta1.ServiceBackendReadyReason, msg), ctrl.Result{}, err
}

func (r *RequestCloneReconciler) patchStatus(ctx context.Context, ph *v1beta1.RequestClone) error {
	key := client.ObjectKeyFromObject(ph)
	latest := &v1beta1.RequestClone{}
	if err := r.Client.Get(ctx, key, latest); err != nil {
		return err
	}

	return r.Client.Status().Patch(ctx, ph, client.MergeFrom(latest))
}

// objectKey returns client.ObjectKey for the object.
func objectKey(object metav1.Object) client.ObjectKey {
	return client.ObjectKey{
		Namespace: object.GetNamespace(),
		Name:      object.GetName(),
	}
}
