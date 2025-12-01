package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	storagev1alpha1 "rclone-s3-operator/api/v1alpha1"
)

const (
	defaultPort         int32 = 9000
	defaultImage              = "rclone/rclone:latest"
	defaultConfigSecret       = "rclone-secret"

	conditionReady = "Ready"
)

// BucketReconciler reconciles a Bucket object.
type BucketReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	TargetNamespace string
}

//+kubebuilder:rbac:groups=storage.max.io,resources=buckets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.max.io,resources=buckets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=storage.max.io,resources=buckets/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services;secrets;events,verbs=get;list;watch;create;update;patch;delete

func (r *BucketReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var instance storagev1alpha1.Bucket
	if err := r.Get(ctx, req.NamespacedName, &instance); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	applyDefaults(&instance)

	targetNamespace := r.TargetNamespace
	if targetNamespace == "" {
		targetNamespace = instance.Namespace
	}

	serviceName := serviceNameFor(&instance)
	secretName := secretNameFor(&instance, serviceName)
	deployName := fmt.Sprintf("%s-deploy", serviceName)

	secret, err := r.reconcileCredentialsSecret(ctx, &instance, instance.Namespace, targetNamespace, serviceName, secretName)
	if err != nil {
		logger.Error(err, "failed reconciling credentials secret")
		return ctrl.Result{}, err
	}

	if err := r.reconcileDeployment(ctx, &instance, targetNamespace, deployName, serviceName, secret); err != nil {
		logger.Error(err, "failed reconciling deployment")
		return ctrl.Result{}, err
	}

	if err := r.reconcileService(ctx, &instance, targetNamespace, serviceName); err != nil {
		logger.Error(err, "failed reconciling service")
		return ctrl.Result{}, err
	}

	if err := r.updateStatusReady(ctx, &instance, serviceName, secretName, targetNamespace); err != nil {
		logger.Error(err, "failed updating status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *BucketReconciler) reconcileCredentialsSecret(ctx context.Context, cr *storagev1alpha1.Bucket, secretNamespace, serviceNamespace, serviceName, secretName string) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		if len(secret.Data["ACCESS_KEY_ID"]) == 0 {
			secret.Data["ACCESS_KEY_ID"] = []byte(randomKey(16))
		}
		if len(secret.Data["SECRET_ACCESS_KEY"]) == 0 {
			secret.Data["SECRET_ACCESS_KEY"] = []byte(randomKey(32))
		}
		endpoint := fmt.Sprintf("http://%s.%s.svc:%d", serviceName, serviceNamespace, portFor(cr))
		secret.Data["url"] = []byte(endpoint)
		// Only set owner reference when the secret is in the same namespace as the CR.
		if secretNamespace == cr.Namespace {
			return controllerutil.SetControllerReference(cr, secret, r.Scheme)
		}
		return nil
	})

	return secret, err
}

func (r *BucketReconciler) reconcileDeployment(ctx context.Context, cr *storagev1alpha1.Bucket, namespace, deployName, serviceName string, creds *corev1.Secret) error {
	labels := baseLabels(cr.Name)
	podLabels := mergeStringMap(labels, cr.Spec.PodLabels)
	podAnnotations := map[string]string{}
	if len(cr.Spec.PodAnnotations) > 0 {
		podAnnotations = mergeStringMap(podAnnotations, cr.Spec.PodAnnotations)
	}
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName,
			Namespace: namespace,
			Labels:    labels,
		},
	}

	port := portFor(cr)
	replicas := replicaCount(cr)
	args := buildServeArgs(cr, port)
	affinity := buildAffinity(cr)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		deploy.Spec.Replicas = ptr.To(replicas)
		deploy.Spec.Template.ObjectMeta.Labels = podLabels
		deploy.Spec.Template.ObjectMeta.Annotations = podAnnotations
		deploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name:            "rclone",
				Image:           imageFor(cr),
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{"/bin/sh", "-c"},
				Args:            []string{strings.Join(args, " ")},
				Ports: []corev1.ContainerPort{{
					Name:          "http",
					ContainerPort: port,
				}},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "rclone-config",
						MountPath: "/etc/rclone",
						ReadOnly:  true,
					},
				},
				Resources: cr.Spec.Resources,
			},
		}
		deploy.Spec.Template.Spec.Affinity = affinity
		deploy.Spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: "rclone-config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: configSecretName(cr),
					},
				},
			},
		}
		if namespace == cr.Namespace {
			return controllerutil.SetControllerReference(cr, deploy, r.Scheme)
		}
		return nil
	})
	return err
}

func (r *BucketReconciler) reconcileService(ctx context.Context, cr *storagev1alpha1.Bucket, namespace, serviceName string) error {
	labels := baseLabels(cr.Name)
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels:    labels,
		},
	}
	port := portFor(cr)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
		desired := corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       port,
					TargetPort: intstr.FromInt(int(port)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		}

		// Preserve immutable fields on updates.
		if service.Spec.ClusterIP != "" {
			desired.ClusterIP = service.Spec.ClusterIP
		}
		if service.Spec.ClusterIPs != nil {
			desired.ClusterIPs = service.Spec.ClusterIPs
		}
		service.Spec = desired
		if namespace == cr.Namespace {
			return controllerutil.SetControllerReference(cr, service, r.Scheme)
		}
		return nil
	})
	return err
}

func (r *BucketReconciler) updateStatusReady(ctx context.Context, cr *storagev1alpha1.Bucket, serviceName, secretName, namespace string) error {
	newStatus := storagev1alpha1.BucketStatus{
		Ready:              true,
		Endpoint:           fmt.Sprintf("http://%s.%s.svc:%d", serviceName, namespace, portFor(cr)),
		SecretName:         secretName,
		ObservedGeneration: cr.Generation,
	}
	meta := metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "ResourcesHealthy",
		Message:            "Deployment, Service, and credentials are present",
	}

	newStatus.Conditions = mergeCondition(cr.Status.Conditions, meta)

	if equality.Semantic.DeepEqual(cr.Status, newStatus) {
		return nil
	}

	cr.Status = newStatus
	return r.Status().Update(ctx, cr)
}

func (r *BucketReconciler) setStatusNotReady(ctx context.Context, cr *storagev1alpha1.Bucket, reason, message string) error {
	meta := metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionFalse,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
	newStatus := storagev1alpha1.BucketStatus{
		Ready:              false,
		Endpoint:           cr.Status.Endpoint,
		SecretName:         cr.Status.SecretName,
		ObservedGeneration: cr.Generation,
		Conditions:         mergeCondition(cr.Status.Conditions, meta),
	}

	if equality.Semantic.DeepEqual(cr.Status, newStatus) {
		return nil
	}

	cr.Status = newStatus
	return r.Status().Update(ctx, cr)
}

func (r *BucketReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&storagev1alpha1.Bucket{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

func applyDefaults(cr *storagev1alpha1.Bucket) {
	if cr.Spec.Port == 0 {
		cr.Spec.Port = defaultPort
	}
	if cr.Spec.Image == "" {
		cr.Spec.Image = defaultImage
	}
	if cr.Spec.ServiceType == "" {
		cr.Spec.ServiceType = corev1.ServiceTypeClusterIP
	}
	if cr.Spec.Replicas == nil {
		cr.Spec.Replicas = ptr.To(int32(1))
	}
}

func portFor(cr *storagev1alpha1.Bucket) int32 {
	if cr.Spec.Port == 0 {
		return defaultPort
	}
	return cr.Spec.Port
}

func imageFor(cr *storagev1alpha1.Bucket) string {
	if cr.Spec.Image == "" {
		return defaultImage
	}
	return cr.Spec.Image
}

func configSecretName(cr *storagev1alpha1.Bucket) string {
	return defaultConfigSecret
}

func replicaCount(cr *storagev1alpha1.Bucket) int32 {
	if cr.Spec.Replicas == nil {
		return 1
	}
	if *cr.Spec.Replicas < 1 {
		return 1
	}
	return *cr.Spec.Replicas
}

func buildServeArgs(cr *storagev1alpha1.Bucket, port int32) []string {
	args := []string{
		"rclone", "serve", "s3", cr.Spec.Remote,
		"--addr", fmt.Sprintf(":%d", port),
		"--config", "/etc/rclone/rclone.conf",
		"--auth-key", "$ACCESS_KEY_ID,$SECRET_ACCESS_KEY",
	}
	if cr.Spec.ReadOnly {
		args = append(args, "--read-only")
	}
	args = append(args, cr.Spec.ExtraArgs...)
	return args
}

func randomKey(length int) string {
	size := length / 2
	if length%2 != 0 {
		size++
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	value := hex.EncodeToString(b)
	if len(value) > length {
		return value[:length]
	}
	return value
}

func baseLabels(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "rclone-s3",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/managed-by": "rclone-s3-operator",
	}
}

func mergeStringMap(base map[string]string, overrides map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

func buildAffinity(cr *storagev1alpha1.Bucket) *corev1.Affinity {
	if cr.Spec.NodeAffinity == nil && cr.Spec.PodAffinity == nil {
		return nil
	}
	return &corev1.Affinity{
		NodeAffinity: cr.Spec.NodeAffinity,
		PodAffinity:  cr.Spec.PodAffinity,
	}
}

func mergeCondition(existing []metav1.Condition, new metav1.Condition) []metav1.Condition {
	updated := sets.NewString()
	result := make([]metav1.Condition, 0, len(existing)+1)
	for _, cond := range existing {
		if cond.Type == new.Type {
			result = append(result, new)
			updated.Insert(cond.Type)
		} else {
			result = append(result, cond)
		}
	}
	if !updated.Has(new.Type) {
		result = append(result, new)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Type < result[j].Type
	})
	return result
}

func serviceNameFor(cr *storagev1alpha1.Bucket) string {
	return fmt.Sprintf("%s-%s-rclone-s3", cr.Name, nameSuffix(cr))
}

func secretNameFor(cr *storagev1alpha1.Bucket, serviceName string) string {
	if cr.Spec.SecretName != "" {
		return cr.Spec.SecretName
	}
	return fmt.Sprintf("%s-credentials", serviceName)
}

func nameSuffix(cr *storagev1alpha1.Bucket) string {
	uid := string(cr.UID)
	if uid == "" {
		return "temp"
	}
	if len(uid) > 6 {
		uid = uid[:6]
	}
	return strings.ToLower(uid)
}
