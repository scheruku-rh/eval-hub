package k8s

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	modelAPIKeySuffix = "_api-key"
	modelURLSuffix    = "_url"
	modelHFTokenKey   = "hf-token"
	modelCACertKey    = "ca_cert"
	modelSingleAPIKey = "api-key"
)

// directAdapterSecretKeys are keys from the model credential secret projected directly into the
// adapter volume rather than replaced with a ref token. These credentials cannot be proxied by
// the sidecar (HF Hub calls bypass the proxy; ca_cert is a TLS artifact, not an inference
// credential), so the adapter receives the real values via a selective model credential secret
// projection alongside the internalModelRef secret.
var directAdapterSecretKeys = []string{modelHFTokenKey, modelCACertKey}

// modelSecretInfo summarises what a model credential secret contains.
type modelSecretInfo struct {
	// directAdapterKeys lists keys that must be projected directly into the adapter volume
	// (hf-token, ca_cert) — present only when those keys exist in the secret.
	directAdapterKeys []string
	// hasCredentialKeys is true when the secret contains at least one key that requires
	// sidecar proxy injection (api-key, *_api-key suffix, or *_url suffix).
	// When false the secret is passthrough-only (e.g. ca_cert alone); no internalModelRef
	// secret is created and no model proxy is started in the sidecar.
	hasCredentialKeys bool
}

// inspectModelSecret reads the model credential secret once and classifies its keys into
// credential keys (needing sidecar proxy injection) and direct adapter keys (projected directly
// into the adapter). The caller uses this to decide whether to create an internalModelRef
// secret and model proxy, and which keys to project into the adapter volume.
func inspectModelSecret(ctx context.Context, namespace, secretName string, helper *KubernetesHelper) (modelSecretInfo, error) {
	secret, err := helper.GetSecret(ctx, namespace, secretName)
	if err != nil {
		return modelSecretInfo{}, fmt.Errorf("get model credential secret %q: %w", secretName, err)
	}
	var info modelSecretInfo
	for k := range secret.Data {
		switch {
		case k == modelSingleAPIKey, strings.HasSuffix(k, modelAPIKeySuffix), strings.HasSuffix(k, modelURLSuffix):
			info.hasCredentialKeys = true
		case k == modelHFTokenKey || k == modelCACertKey:
			info.directAdapterKeys = append(info.directAdapterKeys, k)
		}
	}
	return info, nil
}

// buildInternalModelRefSecret creates the ephemeral ref-token Secret (internalModelRef secret) in namespace.
// It is only called when inspectModelSecret confirmed the model credential secret has credential keys.
//
// It reads the model credential secret identified by realSecretName, then creates a new Secret
// applying the following rules per key:
//
//   - "api-key"          → value becomes "api-key:ref" (sidecar injects real key)
//   - "*_api-key" suffix → value becomes "<key>:ref" (sidecar injects real key)
//   - "*_url" suffix     → value becomes sidecarProxyURL (adapter routes through sidecar)
//   - "hf-token"         → omitted; projected directly from model credential secret into the adapter
//     volume so the real token is available without going through the sidecar proxy
//   - "ca_cert"          → omitted; projected directly from model credential secret into the adapter
//     volume so the adapter can verify TLS
//   - all other keys     → omitted (conservative; avoids leaking unknown fields)
//
// internalModelRef secret contains only synthetic ref/placeholder values — no real credentials.
// Passthrough keys (hf-token, ca_cert) reach the adapter via a projected volume built by the caller.
func buildInternalModelRefSecret(
	ctx context.Context,
	namespace string,
	refSecretName string,
	realSecretName string,
	sidecarProxyURL string,
	labels map[string]string,
	helper *KubernetesHelper,
) (*corev1.Secret, error) {
	realSecret, err := helper.GetSecret(ctx, namespace, realSecretName)
	if err != nil {
		return nil, fmt.Errorf("get real model secret %q: %w", realSecretName, err)
	}
	if len(realSecret.Data) == 0 {
		return nil, fmt.Errorf("real model secret %q has no data keys", realSecretName)
	}

	refData := make(map[string][]byte, len(realSecret.Data))
	for k := range realSecret.Data {
		switch {
		case k == modelSingleAPIKey:
			refData[k] = []byte(k + modelRefValueSuffix)
		case strings.HasSuffix(k, modelAPIKeySuffix):
			refData[k] = []byte(k + modelRefValueSuffix)
		case strings.HasSuffix(k, modelURLSuffix):
			refData[k] = []byte(sidecarProxyURL)
		case k == modelHFTokenKey || k == modelCACertKey:
			// direct adapter: projected directly from model credential secret into the adapter volume
		}
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      refSecretName,
			Namespace: namespace,
			Labels:    labels,
		},
		Data: refData,
	}
	return helper.CreateSecret(ctx, namespace, secret)
}

// modelRefValueSuffix is shared between secret_builder.go and model_proxy.go.
// Defined here so both sides stay in sync without a cross-package import cycle.
const modelRefValueSuffix = ":ref"
