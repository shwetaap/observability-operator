// Copyright: This file is part of korrel8r, released under https://github.com/korrel8r/korrel8r/blob/main/LICENSE

package korrel8r

import (
	"context"
	"time"
	"fmt"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/controller"
//	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"github.com/rhobs/observability-operator/pkg/reconciler"

)

const (
	korrel8rdeployName                  = "korrel8r"
	OperatorNamespace      = "operators"
)

var createOrUpdateDeployment = builder.WithPredicates(predicate.Funcs{
	UpdateFunc:  func(e event.UpdateEvent) bool { 
		return e.ObjectNew.GetNamespace() == OperatorNamespace },
	CreateFunc:  func(e event.CreateEvent) bool { 
		return e.Object.GetNamespace() == OperatorNamespace },
	DeleteFunc:  func(e event.DeleteEvent) bool { return false },
	GenericFunc: func(e event.GenericEvent) bool { return false },
})

// Korrel8rReconciler reconciles a Korrel8r object
type resourceManager struct {
	k8sClient  client.Client
	scheme     *runtime.Scheme
	logger     logr.Logger
	controller controller.Controller
	korrel8rconf Korrel8rConfiguration
}

type Korrel8rConfiguration struct {
	Image string
}

// Options allows for controller options to be set
type Options struct {
	Korrel8rconf Korrel8rConfiguration
}
// List permissions needed by Reconcile - used to generate role.yaml
//
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
//

// RegisterWithManager registers the controller with Manager
func RegisterWithManager(mgr ctrl.Manager, opts Options) error {

	rm := &resourceManager{
		k8sClient:    mgr.GetClient(),
		scheme:       mgr.GetScheme(),
		logger:       ctrl.Log.WithName("observability-operator"),
		korrel8rconf: opts.Korrel8rconf,
	}

	ctrl, err := ctrl.NewControllerManagedBy(mgr).
		Named("Korrel8r").
		//Watches(&msoapi.MonitoringStack{}, &handler.EnqueueRequestForObject{}).
		Watches(&corev1.Service{}, &handler.EnqueueRequestForObject{}, createOrUpdateDeployment).
		Watches(&appsv1.Deployment{}, &handler.EnqueueRequestForObject{}, createOrUpdateDeployment).
		Build(rm)

	if err != nil {
		return err
	}
	rm.controller = ctrl
	return nil
}

func (rm resourceManager) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := rm.logger.WithValues("stack", req.NamespacedName)
	logger.Info("Reconciling Korrel8r stack")


	korrel8rSvc := &corev1.Service{}
	err := rm.k8sClient.Get(ctx, client.ObjectKey{
		Namespace: req.NamespacedName.Namespace,
		Name:      korrel8rdeployName,
	}, korrel8rSvc);
	if err != nil && !apierrors.IsNotFound(err){
		return ctrl.Result{}, err
	}
	korrel8rSvcStr := fmt.Sprintf("%+v", *korrel8rSvc)
	logger.Info("korrel8r value ", korrel8rSvc, korrel8rSvcStr)
	if korrel8rSvc == nil {
		logger.Info("Service not found creating korrel svc", korrel8rSvc, korrel8rSvcStr)
		korrel8rNewSvc := newKorrel8rService(korrel8rdeployName, req.NamespacedName.Namespace)
		if err := reconciler.NewMerger(korrel8rNewSvc).Reconcile(ctx, rm.k8sClient, rm.scheme); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	korrel8rDeploy := &appsv1.Deployment{}
	err = rm.k8sClient.Get(ctx, client.ObjectKey{
		Namespace: req.NamespacedName.Namespace,
		Name:      korrel8rdeployName,
	}, korrel8rDeploy);
	if err != nil && !apierrors.IsNotFound(err){
		return ctrl.Result{}, err
	}
	korrel8rDeployStr := fmt.Sprintf("%+v", *korrel8rDeploy)
	logger.Info("korrel8r value ", korrel8rDeploy, korrel8rDeployStr)
	if korrel8rDeploy == nil {
		logger.Info("Deployment not found creating korrel ddeply ")
		korrel8rNewDeploy := newKorrel8rDeployment(korrel8rdeployName, req.NamespacedName.Namespace, rm.korrel8rconf)
		if err := reconciler.NewMerger(korrel8rNewDeploy).Reconcile(ctx, rm.k8sClient, rm.scheme); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	reconcilers := korrel8rComponentReconcilers(korrel8rDeploy, korrel8rSvc, rm.korrel8rconf, OperatorNamespace)
	for _, reconciler := range reconcilers {
		err := reconciler.Reconcile(ctx, rm.k8sClient, rm.scheme)
		// handle creation / updation errors that can happen due to a stale cache by
		// retrying after some time.
		if apierrors.IsAlreadyExists(err) || apierrors.IsConflict(err) {
			logger.V(8).Info("skipping reconcile error", "err", err)
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}
