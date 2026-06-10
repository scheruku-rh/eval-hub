package k8s

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/eval-hub/eval-hub/internal/eval_hub/abstractions"
	"github.com/eval-hub/eval-hub/internal/eval_hub/config"
	"github.com/eval-hub/eval-hub/internal/eval_hub/handlers"
	"github.com/eval-hub/eval-hub/pkg/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type fakeStorage struct {
	logger            *slog.Logger
	called            bool
	ctx               context.Context
	runStatus         *api.StatusEvent
	runStatusChan     chan *api.StatusEvent
	updateErr         error
	tenant            api.Tenant
	owner             api.User
	providerConfigs   map[string]api.ProviderResource
	collectionConfigs map[string]api.CollectionResource
}

// UpdateEvaluationJob implements [abstractions.Storage].
func (f *fakeStorage) UpdateEvaluationJob(id string, runStatus *api.StatusEvent) error {
	f.called = true
	f.runStatus = runStatus
	if f.runStatusChan != nil {
		select {
		case f.runStatusChan <- runStatus:
		default:
		}
	}
	return f.updateErr
}

func (f *fakeStorage) Ping(_ time.Duration) error { return nil }
func (f *fakeStorage) CreateEvaluationJob(_ *api.EvaluationJobResource) error {
	return nil
}
func (f *fakeStorage) GetEvaluationJob(_ string) (*api.EvaluationJobResource, error) {
	return nil, nil
}
func (f *fakeStorage) GetEvaluationJobs(_ *abstractions.QueryFilter) (*abstractions.QueryResults[api.EvaluationJobResource], error) {
	return nil, nil
}
func (f *fakeStorage) DeleteEvaluationJob(_ string) error {
	return nil
}
func (f *fakeStorage) UpdateEvaluationJobStatus(_ string, _ api.OverallState, _ *api.MessageInfo) error {
	f.called = true
	return nil
}
func (f *fakeStorage) CreateCollection(_ *api.CollectionResource) error {
	return nil
}
func (f *fakeStorage) GetCollection(id string) (*api.CollectionResource, error) {
	if cr, ok := f.collectionConfigs[id]; ok {
		return &cr, nil
	}
	return nil, fmt.Errorf("collection %q not found", id)
}
func (f *fakeStorage) GetCollections(_ *abstractions.QueryFilter) (*abstractions.QueryResults[api.CollectionResource], error) {
	return nil, nil
}
func (f *fakeStorage) UpdateCollection(_ string, _ *api.CollectionConfig) (*api.CollectionResource, error) {
	return nil, nil
}
func (f *fakeStorage) PatchCollection(_ string, _ *api.Patch) (*api.CollectionResource, error) {
	return nil, nil
}
func (f *fakeStorage) DeleteCollection(_ string) error {
	return nil
}
func (f *fakeStorage) CreateProvider(_ *api.ProviderResource) error {
	return nil
}
func (f *fakeStorage) GetProvider(id string) (*api.ProviderResource, error) {
	if pr, ok := f.providerConfigs[id]; ok {
		return &pr, nil
	}
	return nil, fmt.Errorf("provider %q not found", id)
}
func (f *fakeStorage) DeleteProvider(_ string) error {
	return nil
}
func (f *fakeStorage) GetProviders(_ *abstractions.QueryFilter) (*abstractions.QueryResults[api.ProviderResource], error) {
	return nil, nil
}
func (f *fakeStorage) UpdateProvider(_ string, _ *api.ProviderConfig) (*api.ProviderResource, error) {
	return nil, nil
}
func (f *fakeStorage) PatchProvider(_ string, _ *api.Patch) (*api.ProviderResource, error) {
	return nil, nil
}
func (f *fakeStorage) Close() error { return nil }
func (f *fakeStorage) LoadSystemResources(_ map[string]api.CollectionResource, _ map[string]api.ProviderResource) error {
	return nil
}

func (f *fakeStorage) WithLogger(logger *slog.Logger) abstractions.Storage {
	return &fakeStorage{
		logger:            logger,
		ctx:               f.ctx,
		runStatusChan:     f.runStatusChan,
		updateErr:         f.updateErr,
		tenant:            f.tenant,
		owner:             f.owner,
		providerConfigs:   f.providerConfigs,
		collectionConfigs: f.collectionConfigs,
	}
}

func (f *fakeStorage) WithContext(ctx context.Context) abstractions.Storage {
	return &fakeStorage{
		logger:            f.logger,
		ctx:               ctx,
		runStatusChan:     f.runStatusChan,
		updateErr:         f.updateErr,
		tenant:            f.tenant,
		owner:             f.owner,
		providerConfigs:   f.providerConfigs,
		collectionConfigs: f.collectionConfigs,
	}
}

func (f *fakeStorage) WithTenant(tenant api.Tenant) abstractions.Storage {
	return &fakeStorage{
		logger:            f.logger,
		ctx:               f.ctx,
		runStatusChan:     f.runStatusChan,
		updateErr:         f.updateErr,
		tenant:            tenant,
		owner:             f.owner,
		providerConfigs:   f.providerConfigs,
		collectionConfigs: f.collectionConfigs,
	}
}

func (f *fakeStorage) WithOwner(owner api.User) abstractions.Storage {
	return &fakeStorage{
		logger:            f.logger,
		ctx:               f.ctx,
		runStatusChan:     f.runStatusChan,
		updateErr:         f.updateErr,
		tenant:            f.tenant,
		owner:             owner,
		providerConfigs:   f.providerConfigs,
		collectionConfigs: f.collectionConfigs,
	}
}

func TestK8sRuntimeName(t *testing.T) {
	runtime := &K8sRuntime{}
	if runtime.Name() != "kubernetes" {
		t.Fatalf("expected Name to be kubernetes")
	}
}

func TestCreateBenchmarkResourcesSetsConfigMapOwner(t *testing.T) {
	providerID := "provider-1"
	evaluation := sampleEvaluation(providerID)

	clientset := fake.NewClientset()
	runtime := &K8sRuntime{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		helper: &KubernetesHelper{clientset: clientset},
		serviceConfig: &config.Config{
			Service: &config.ServiceConfig{
				EvalInitImage: "eval-init-image",
			},
		},
	}

	storage := &fakeStorage{providerConfigs: sampleProviders(providerID)}
	err := runtime.createBenchmarkResources(context.Background(), runtime.logger, evaluation, &evaluation.Benchmarks[0], 0, storage)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	configMaps := listConfigMapsByJobID(t, clientset, evaluation.Resource.ID)
	if len(configMaps) != 1 {
		t.Fatalf("expected 1 configmap, got %d", len(configMaps))
	}
	cm := configMaps[0]
	if len(cm.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(cm.OwnerReferences))
	}
	owner := cm.OwnerReferences[0]
	if owner.Kind != "Job" || owner.APIVersion != "batch/v1" {
		t.Fatalf("expected owner to be batch/v1 Job, got %s %s", owner.APIVersion, owner.Kind)
	}
	jobs := listJobsByJobID(t, clientset, evaluation.Resource.ID)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if owner.Name != jobs[0].Name {
		t.Fatalf("expected owner name to match job name, got %q", owner.Name)
	}
	if owner.Controller == nil || !*owner.Controller {
		t.Fatalf("expected owner reference to be controller")
	}
}

func TestCreateBenchmarkResourcesSetsAnnotations(t *testing.T) {
	providerID := "provider-1"
	evaluation := sampleEvaluation(providerID)

	clientset := fake.NewClientset()
	runtime := &K8sRuntime{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		helper: &KubernetesHelper{clientset: clientset},
		serviceConfig: &config.Config{
			Service: &config.ServiceConfig{
				EvalInitImage: "eval-init-image",
			},
		},
	}

	storage := &fakeStorage{providerConfigs: sampleProviders(providerID)}
	err := runtime.createBenchmarkResources(context.Background(), runtime.logger, evaluation, &evaluation.Benchmarks[0], 0, storage)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	configMaps := listConfigMapsByJobID(t, clientset, evaluation.Resource.ID)
	if len(configMaps) != 1 {
		t.Fatalf("expected 1 configmap, got %d", len(configMaps))
	}
	cm := configMaps[0]
	if cm.Annotations[annotationJobIDKey] != evaluation.Resource.ID {
		t.Fatalf("expected configmap job_id annotation %q, got %q", evaluation.Resource.ID, cm.Annotations[annotationJobIDKey])
	}
	if cm.Annotations[annotationProviderIDKey] != evaluation.Benchmarks[0].ProviderID {
		t.Fatalf("expected configmap provider_id annotation %q, got %q", evaluation.Benchmarks[0].ProviderID, cm.Annotations[annotationProviderIDKey])
	}
	if cm.Annotations[annotationBenchmarkIDKey] != evaluation.Benchmarks[0].ID {
		t.Fatalf("expected configmap benchmark_id annotation %q, got %q", evaluation.Benchmarks[0].ID, cm.Annotations[annotationBenchmarkIDKey])
	}

	jobs := listJobsByJobID(t, clientset, evaluation.Resource.ID)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	job := jobs[0]
	if job.Annotations[annotationJobIDKey] != evaluation.Resource.ID {
		t.Fatalf("expected job job_id annotation %q, got %q", evaluation.Resource.ID, job.Annotations[annotationJobIDKey])
	}
	if job.Annotations[annotationProviderIDKey] != evaluation.Benchmarks[0].ProviderID {
		t.Fatalf("expected job provider_id annotation %q, got %q", evaluation.Benchmarks[0].ProviderID, job.Annotations[annotationProviderIDKey])
	}
	if job.Annotations[annotationBenchmarkIDKey] != evaluation.Benchmarks[0].ID {
		t.Fatalf("expected job benchmark_id annotation %q, got %q", evaluation.Benchmarks[0].ID, job.Annotations[annotationBenchmarkIDKey])
	}
	if job.Spec.Template.Annotations[annotationJobIDKey] != evaluation.Resource.ID {
		t.Fatalf("expected pod job_id annotation %q, got %q", evaluation.Resource.ID, job.Spec.Template.Annotations[annotationJobIDKey])
	}
	if job.Spec.Template.Annotations[annotationProviderIDKey] != evaluation.Benchmarks[0].ProviderID {
		t.Fatalf("expected pod provider_id annotation %q, got %q", evaluation.Benchmarks[0].ProviderID, job.Spec.Template.Annotations[annotationProviderIDKey])
	}
	if job.Spec.Template.Annotations[annotationBenchmarkIDKey] != evaluation.Benchmarks[0].ID {
		t.Fatalf("expected pod benchmark_id annotation %q, got %q", evaluation.Benchmarks[0].ID, job.Spec.Template.Annotations[annotationBenchmarkIDKey])
	}
}

func TestCreateBenchmarkResourcesAddsModelAuthVolumeAndEnv(t *testing.T) {
	providerID := "provider-1"
	evaluation := sampleEvaluation(providerID)
	evaluation.Model.Auth = &api.ModelAuth{SecretRef: "model-auth-secret"}

	// Pre-create the real model auth secret (model credential secret) so the runtime can read its keys
	// to generate the ephemeral ref-token secret (internalModelRef secret).
	// Includes ca_cert and hf-token (both excluded from internalModelRef secret, projected directly from model credential secret).
	realSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-auth-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"api-key":  []byte("sk-real-key"),
			"hf-token": []byte("hf-real-token"),
			"ca_cert":  []byte("-----BEGIN CERTIFICATE-----"),
		},
	}
	clientset := fake.NewClientset(realSecret)
	runtime := &K8sRuntime{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		helper: &KubernetesHelper{clientset: clientset},
		serviceConfig: &config.Config{
			Service: &config.ServiceConfig{
				EvalInitImage: "eval-init-image",
			},
		},
	}

	storage := &fakeStorage{providerConfigs: sampleProviders(providerID)}
	err := runtime.createBenchmarkResources(context.Background(), runtime.logger, evaluation, &evaluation.Benchmarks[0], 0, storage)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	jobs := listJobsByJobID(t, clientset, evaluation.Resource.ID)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	job := jobs[0]
	container := job.Spec.Template.Spec.Containers[0]

	// Adapter container: volume must be a projected volume combining internalModelRef secret (ref keys)
	// and a selective projection of model credential secret (hf-token, ca_cert only).
	var foundVolume bool
	var adapterRefSecretName string
	for _, volume := range job.Spec.Template.Spec.Volumes {
		if volume.Name == modelAuthVolumeName {
			foundVolume = true
			if volume.VolumeSource.Projected == nil {
				t.Fatalf("expected model auth volume to be a projected volume, got plain Secret")
			}
			sources := volume.VolumeSource.Projected.Sources
			if len(sources) < 2 {
				t.Fatalf("expected at least 2 projected sources (internalModelRef secret + model credential secret passthrough), got %d", len(sources))
			}
			// First source: internalModelRef secret (ref secret — no items filter means all keys)
			if sources[0].Secret == nil {
				t.Fatal("expected first projected source to be a Secret (internalModelRef secret)")
			}
			if sources[0].Secret.LocalObjectReference.Name == "model-auth-secret" {
				t.Fatal("first projected source must be the ephemeral ref secret, not the real secret")
			}
			adapterRefSecretName = sources[0].Secret.LocalObjectReference.Name
			// Second source: model credential secret selective projection (hf-token, ca_cert)
			if sources[1].Secret == nil {
				t.Fatal("expected second projected source to be a Secret (model credential secret selective)")
			}
			if sources[1].Secret.LocalObjectReference.Name != "model-auth-secret" {
				t.Fatalf("expected second projected source to be the real secret %q, got %q", "model-auth-secret", sources[1].Secret.LocalObjectReference.Name)
			}
			projectedKeys := make(map[string]bool)
			for _, item := range sources[1].Secret.Items {
				projectedKeys[item.Key] = true
			}
			if !projectedKeys["hf-token"] {
				t.Fatal("expected hf-token to be projected from model credential secret into adapter volume")
			}
			if !projectedKeys["ca_cert"] {
				t.Fatal("expected ca_cert to be projected from model credential secret into adapter volume")
			}
		}
	}
	if !foundVolume {
		t.Fatalf("expected volume %s to be present on adapter", modelAuthVolumeName)
	}

	var foundMount bool
	for _, mount := range container.VolumeMounts {
		if mount.Name == modelAuthVolumeName {
			foundMount = true
			if mount.MountPath != modelAuthMountPath {
				t.Fatalf("expected mount path %q, got %q", modelAuthMountPath, mount.MountPath)
			}
		}
	}
	if !foundMount {
		t.Fatalf("expected volume mount %s on adapter container", modelAuthVolumeName)
	}

	// internalModelRef secret (ref secret) must exist with ref keys only — no hf-token, no ca_cert.
	if adapterRefSecretName == "" {
		t.Fatal("expected a non-empty ref secret name from the projected volume")
	}
	refSecret, err := clientset.CoreV1().Secrets("default").Get(context.Background(), adapterRefSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected ephemeral ref secret %q to exist, got error: %v", adapterRefSecretName, err)
	}
	if string(refSecret.Data["api-key"]) != "api-key:ref" {
		t.Fatalf("expected ref secret api-key value %q, got %q", "api-key:ref", string(refSecret.Data["api-key"]))
	}
	if _, ok := refSecret.Data["hf-token"]; ok {
		t.Fatal("hf-token must not appear in internalModelRef secret; it is projected directly from model credential secret")
	}
	if _, ok := refSecret.Data["ca_cert"]; ok {
		t.Fatal("ca_cert must not appear in internalModelRef secret; it is projected directly from model credential secret")
	}

	// Sidecar container: should have the real secret (model credential secret) mounted at modelAuthRealMountPath.
	sidecarContainer := findContainer(job.Spec.Template.Spec.InitContainers, sidecarContainerName)
	if sidecarContainer == nil {
		t.Fatal("expected sidecar init container")
	}
	var foundRealMount bool
	for _, mount := range sidecarContainer.VolumeMounts {
		if mount.Name == modelAuthRealVolumeName {
			foundRealMount = true
			if mount.MountPath != modelAuthRealMountPath {
				t.Fatalf("expected sidecar real secret mount path %q, got %q", modelAuthRealMountPath, mount.MountPath)
			}
		}
	}
	if !foundRealMount {
		t.Fatalf("expected sidecar to have %s mount at %s", modelAuthRealVolumeName, modelAuthRealMountPath)
	}
	var foundRealVolume bool
	for _, volume := range job.Spec.Template.Spec.Volumes {
		if volume.Name == modelAuthRealVolumeName {
			foundRealVolume = true
			if volume.VolumeSource.Secret == nil || volume.VolumeSource.Secret.SecretName != "model-auth-secret" {
				t.Fatalf("expected sidecar real secret volume to reference %q", "model-auth-secret")
			}
		}
	}
	if !foundRealVolume {
		t.Fatalf("expected volume %s (real creds) to be present for sidecar", modelAuthRealVolumeName)
	}

	// Legacy model auth env vars should be absent.
	envKeys := make(map[string]struct{}, len(container.Env))
	for _, env := range container.Env {
		envKeys[env.Name] = struct{}{}
	}
	legacyModelAuthKeys := []string{
		"MODEL_AUTH_API_KEY_PATH",
		"MODEL_AUTH_CA_CERT_PATH",
	}
	for _, key := range legacyModelAuthKeys {
		if _, found := envKeys[key]; found {
			t.Fatalf("expected env var %s to be absent", key)
		}
	}
}

func TestBuildModelRefSecretMultiModel(t *testing.T) {
	// Multi-model secret: *_api-key keys become refs, *_url keys become the sidecar proxy URL,
	// ca_cert is excluded.
	realSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-model-secret", Namespace: "default"},
		Data: map[string][]byte{
			"model-1_api-key": []byte("sk-model1"),
			"model-1_url":     []byte("https://api.openai.com/v1"),
			"model-2_api-key": []byte("sk-model2"),
			"model-2_url":     []byte("https://azure.example.com/v1"),
			"ca_cert":         []byte("-----BEGIN CERTIFICATE-----"),
		},
	}
	clientset := fake.NewClientset(realSecret)
	helper := &KubernetesHelper{clientset: clientset}

	const sidecarURL = "http://localhost:8080"
	secret, err := buildInternalModelRefSecret(context.Background(), "default", "multi-model-ref", "multi-model-secret", sidecarURL, nil, helper)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cases := map[string]string{
		"model-1_api-key": "model-1_api-key:ref",
		"model-2_api-key": "model-2_api-key:ref",
		"model-1_url":     sidecarURL,
		"model-2_url":     sidecarURL,
	}
	for k, want := range cases {
		got := string(secret.Data[k])
		if got != want {
			t.Errorf("key %q: want %q, got %q", k, want, got)
		}
	}
	if _, ok := secret.Data["ca_cert"]; ok {
		t.Error("ca_cert must not appear in the ref secret")
	}
}

func TestInspectModelSecretPassthroughOnly(t *testing.T) {
	// A secret with only ca_cert (and an unknown key) has no credential keys.
	// inspectModelSecret should report hasCredentialKeys=false and directAdapterKeys=["ca_cert"].
	// k8s_runtime uses this to skip creating an internalModelRef secret and model proxy,
	// mounting the real secret directly into the adapter instead (e.g. SA token + custom TLS).
	realSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tls-only-creds", Namespace: "default"},
		Data: map[string][]byte{
			"ca_cert":        []byte("-----BEGIN CERTIFICATE-----"),
			"some-other-key": []byte("value"),
		},
	}
	clientset := fake.NewClientset(realSecret)
	helper := &KubernetesHelper{clientset: clientset}

	info, err := inspectModelSecret(context.Background(), "default", "tls-only-creds", helper)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.hasCredentialKeys {
		t.Error("expected hasCredentialKeys=false for ca_cert-only secret")
	}
	if len(info.directAdapterKeys) != 1 || info.directAdapterKeys[0] != "ca_cert" {
		t.Errorf("expected directAdapterKeys=[ca_cert], got %v", info.directAdapterKeys)
	}
}

func TestCreateBenchmarkResourcesAddsInitContainerForS3TestData(t *testing.T) {
	providerID := "provider-1"
	evaluation := sampleEvaluation(providerID)
	evaluation.Benchmarks[0].TestDataRef = &api.TestDataRef{
		S3: &api.S3TestDataRef{
			Bucket:    "bucket-1",
			Key:       "/a/b",
			SecretRef: "s3-secret",
		},
	}

	clientset := fake.NewClientset()
	runtime := &K8sRuntime{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		helper: &KubernetesHelper{clientset: clientset},
		serviceConfig: &config.Config{
			Service: &config.ServiceConfig{
				EvalInitImage: "eval-init-image",
			},
		},
	}

	storage := &fakeStorage{providerConfigs: sampleProviders(providerID)}
	err := runtime.createBenchmarkResources(context.Background(), runtime.logger, evaluation, &evaluation.Benchmarks[0], 0, storage)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	jobs := listJobsByJobID(t, clientset, evaluation.Resource.ID)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	job := jobs[0]
	initContainer := findContainer(job.Spec.Template.Spec.InitContainers, initContainerName)
	if initContainer == nil {
		t.Fatal("expected test-data init container")
	}
	if len(initContainer.Command) != 1 || initContainer.Command[0] != defaultTestDataInitCmd {
		t.Fatalf("expected init container command %q, got %v", defaultTestDataInitCmd, initContainer.Command)
	}

	var foundBucketEnv, foundKeyEnv bool
	for _, env := range initContainer.Env {
		if env.Name == envTestDataS3BucketName {
			foundBucketEnv = true
			if env.Value != "bucket-1" {
				t.Fatalf("expected bucket env %q, got %q", "bucket-1", env.Value)
			}
		}
		if env.Name == envTestDataS3KeyName {
			foundKeyEnv = true
			if env.Value != "a/b" {
				t.Fatalf("expected key env %q, got %q", "a/b", env.Value)
			}
		}
	}
	if !foundBucketEnv || !foundKeyEnv {
		t.Fatalf("expected bucket/key env vars on init container")
	}

	var foundTestDataVolume, foundSecretVolume bool
	for _, volume := range job.Spec.Template.Spec.Volumes {
		if volume.Name == testDataVolumeName {
			foundTestDataVolume = true
		}
		if volume.Name == testDataSecretVolumeName {
			foundSecretVolume = true
			if volume.VolumeSource.Secret == nil || volume.VolumeSource.Secret.SecretName != "s3-secret" {
				t.Fatalf("expected secret volume %q with secret %q", testDataSecretVolumeName, "s3-secret")
			}
		}
	}
	if !foundTestDataVolume || !foundSecretVolume {
		t.Fatalf("expected test data and secret volumes to be present")
	}

	var foundInitMounts bool
	for _, mount := range initContainer.VolumeMounts {
		if mount.Name == testDataVolumeName && mount.MountPath == testDataMountPath {
			foundInitMounts = true
		}
	}
	if !foundInitMounts {
		t.Fatalf("expected init container to mount %s", testDataMountPath)
	}

	var foundAdapterMount bool
	for _, mount := range job.Spec.Template.Spec.Containers[0].VolumeMounts {
		if mount.Name == testDataVolumeName && mount.MountPath == testDataMountPath {
			foundAdapterMount = true
		}
	}
	if !foundAdapterMount {
		t.Fatalf("expected adapter container to mount %s", testDataMountPath)
	}
}

func TestCreateBenchmarkResourcesDeletesConfigMapOnJobFailure(t *testing.T) {
	providerID := "provider-1"
	evaluation := sampleEvaluation(providerID)

	clientset := fake.NewClientset()
	clientset.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, fmt.Errorf("job create failed")
	})

	runtime := &K8sRuntime{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		helper: &KubernetesHelper{clientset: clientset},
		serviceConfig: &config.Config{
			Service: &config.ServiceConfig{
				EvalInitImage: "eval-init-image",
			},
		},
	}

	storage := &fakeStorage{providerConfigs: sampleProviders(providerID)}
	err := runtime.createBenchmarkResources(context.Background(), runtime.logger, evaluation, &evaluation.Benchmarks[0], 0, storage)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	configMaps := listConfigMapsByJobID(t, clientset, evaluation.Resource.ID)
	if len(configMaps) != 0 {
		t.Fatalf("expected configmap to be deleted, got %d", len(configMaps))
	}
}

func TestRunEvaluationJobMarksBenchmarkFailedOnCreateError(t *testing.T) {
	providerID := "provider-1"
	evaluation := sampleEvaluation(providerID)

	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "configmaps", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, fmt.Errorf("configmap create failed")
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runtime := &K8sRuntime{
		logger: logger,
		helper: &KubernetesHelper{clientset: clientset},
		ctx:    context.Background(),
		serviceConfig: &config.Config{
			Service: &config.ServiceConfig{
				EvalInitImage: "eval-init-image",
			},
		},
	}

	statusCh := make(chan *api.StatusEvent, 1)
	storage := &fakeStorage{logger: logger, ctx: context.Background(), runStatusChan: statusCh, providerConfigs: sampleProviders(providerID)}
	var store abstractions.Storage = storage

	benchmarks, err := handlers.GetJobBenchmarks(evaluation, nil)
	if err != nil {
		t.Fatalf("RunEvaluationJob failed to resolve benchmarks: %v", err)
	}

	if err := runtime.RunEvaluationJob(evaluation, benchmarks, store); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	select {
	case runStatus := <-statusCh:
		if runStatus == nil {
			t.Fatalf("expected run status, got nil")
		}
		if runStatus.BenchmarkStatusEvent.Status != api.StateFailed {
			t.Fatalf("expected status failed, got %s", runStatus.BenchmarkStatusEvent.Status)
		}
		if runStatus.BenchmarkStatusEvent.ID != evaluation.Benchmarks[0].ID {
			t.Fatalf("expected benchmark ID %q, got %q", evaluation.Benchmarks[0].ID, runStatus.BenchmarkStatusEvent.ID)
		}
		if runStatus.BenchmarkStatusEvent.ProviderID != evaluation.Benchmarks[0].ProviderID {
			t.Fatalf("expected provider ID %q, got %q", evaluation.Benchmarks[0].ProviderID, runStatus.BenchmarkStatusEvent.ProviderID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected UpdateEvaluationJob to be called")
	}
}

func TestRunEvaluationJobHandlesUpdateFailure(t *testing.T) {
	providerID := "provider-1"
	evaluation := sampleEvaluation(providerID)

	clientset := fake.NewSimpleClientset()
	clientset.PrependReactor("create", "configmaps", func(action k8stesting.Action) (bool, k8sruntime.Object, error) {
		return true, nil, fmt.Errorf("configmap create failed")
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	runtime := &K8sRuntime{
		logger: logger,
		helper: &KubernetesHelper{clientset: clientset},
		ctx:    context.Background(),
		serviceConfig: &config.Config{
			Service: &config.ServiceConfig{
				EvalInitImage: "eval-init-image",
			},
		},
	}

	statusCh := make(chan *api.StatusEvent, 1)
	storage := &fakeStorage{
		logger:          logger,
		ctx:             context.Background(),
		runStatusChan:   statusCh,
		updateErr:       fmt.Errorf("update failed"),
		providerConfigs: sampleProviders(providerID),
	}
	var store abstractions.Storage = storage

	benchmarks, err := handlers.GetJobBenchmarks(evaluation, nil)
	if err != nil {
		t.Fatalf("RunEvaluationJob failed to resolve benchmarks: %v", err)
	}

	if err := runtime.RunEvaluationJob(evaluation, benchmarks, store); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	select {
	case runStatus := <-statusCh:
		if runStatus == nil {
			t.Fatalf("expected run status, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected UpdateEvaluationJob to be called")
	}
}

func sampleEvaluation(providerID string) *api.EvaluationJobResource {
	return &api.EvaluationJobResource{
		Resource: api.EvaluationResource{
			Resource: api.Resource{ID: "job-1"},
		},
		EvaluationJobConfig: api.EvaluationJobConfig{
			Model: api.ModelRef{
				URL:  "http://model.example",
				Name: "model-1",
			},
			Benchmarks: []api.EvaluationBenchmarkConfig{
				{
					Ref: api.Ref{ID: "bench-1"},
					Parameters: map[string]any{
						"foo":          "bar",
						"num_examples": 5,
					},
					ProviderID: providerID,
				},
			},
			Experiment: &api.ExperimentConfig{
				Name: "exp-1",
			},
		},
	}
}

func sampleProviders(providerID string) map[string]api.ProviderResource {
	return map[string]api.ProviderResource{
		providerID: {
			Resource: api.Resource{ID: providerID},
			ProviderConfig: api.ProviderConfig{
				Runtime: &api.Runtime{
					K8s: &api.K8sRuntime{
						Image: "quay.io/evalhub/adapter:latest",
					},
				},
			},
		},
	}
}
