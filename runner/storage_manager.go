package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/flux-iac/tofu-controller/api/plan"
	infrav1 "github.com/flux-iac/tofu-controller/api/v1alpha2"
	"github.com/flux-iac/tofu-controller/utils"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// StorageManager handles storage operations for Terraform state and plan files
type StorageManager struct {
	Client    client.Client
	terraform *infrav1.Terraform
	log       logr.Logger
}

func NewStorageManager(client client.Client, terraform *infrav1.Terraform, log logr.Logger) *StorageManager {
	return &StorageManager{
		Client:    client,
		terraform: terraform,
		log:       log,
	}
}

// getStorageConfig returns the storage configuration with defaults
func (sm *StorageManager) getStorageConfig() *infrav1.StorageConfigSpec {
	if sm.terraform.Spec.StorageConfig == nil {
		// Return default configuration
		maxSecretSize := int64(900000) // 900KB
		return &infrav1.StorageConfigSpec{
			Type:            infrav1.StorageTypeSecret,
			MaxSecretSize:   &maxSecretSize,
			AutoFallback:    false,
			VolumeMountPath: "/tmp/tf-storage",
		}
	}
	return sm.terraform.Spec.StorageConfig
}

// shouldUseVolumeStorage determines if volume storage should be used based on data size and configuration
func (sm *StorageManager) shouldUseVolumeStorage(dataSize int64) bool {
	config := sm.getStorageConfig()

	switch config.Type {
	case infrav1.StorageTypeVolume:
		return true
	case infrav1.StorageTypeSecret:
		if config.AutoFallback {
			maxSize := int64(900000) // default 900KB
			if config.MaxSecretSize != nil {
				maxSize = *config.MaxSecretSize
			}
			return dataSize > maxSize
		}
		return false
	default:
		return false
	}
}

// WriteTFPlan writes the Terraform plan using the configured storage method
func (sm *StorageManager) WriteTFPlan(ctx context.Context, name, namespace, planId, suffix, uuid string, data []byte) error {
	dataSize := int64(len(data))
	sm.log.Info("writing terraform plan", "size", dataSize, "planId", planId)

	// Determine storage method
	useVolumeStorage := sm.shouldUseVolumeStorage(dataSize)

	if useVolumeStorage {
		return sm.writePlanToVolume(ctx, name, namespace, planId, suffix, uuid, data)
	} else {
		// Use chunked secret storage for plans that fit
		return sm.writePlanAsChunkedSecrets(ctx, name, namespace, planId, uuid, data)
	}
}

// writePlanAsChunkedSecrets writes the plan as one or more chunked secrets
func (sm *StorageManager) writePlanAsChunkedSecrets(ctx context.Context, name, namespace, planId, uuid string, data []byte) error {
	// Create plan object
	p, err := plan.NewFromBytes(name, namespace, sm.terraform.WorkspaceName(), uuid, planId, data)
	if err != nil {
		return fmt.Errorf("failed to create plan object: %w", err)
	}

	// Convert to secrets (pass empty suffix since we don't use it)
	secretPtrs, err := p.ToSecret("")
	if err != nil {
		return fmt.Errorf("failed to convert plan to secrets: %w", err)
	}

	// Convert []*v1.Secret to []v1.Secret for easier handling
	secrets := make([]v1.Secret, len(secretPtrs))
	for i, s := range secretPtrs {
		secrets[i] = *s
	}

	// Delete any existing plan secrets for this terraform object
	secretList := &v1.SecretList{}
	if err := sm.Client.List(ctx, secretList, client.InNamespace(namespace), client.MatchingLabels{
		"infra.contrib.fluxcd.io/plan-name":      name,
		"infra.contrib.fluxcd.io/plan-workspace": sm.terraform.WorkspaceName(),
	}); err != nil {
		sm.log.Error(err, "unable to list existing plan secrets")
	} else {
		for _, secret := range secretList.Items {
			if err := sm.Client.Delete(ctx, &secret); err != nil && !errors.IsNotFound(err) {
				sm.log.Error(err, "unable to delete old plan secret", "secret", secret.Name)
			}
		}
	}

	// Create new plan secrets
	for i := range secrets {
		if err := sm.Client.Create(ctx, &secrets[i]); err != nil {
			return fmt.Errorf("failed to create plan secret %s: %w", secrets[i].Name, err)
		}
	}

	sm.log.Info("wrote plan as chunked secrets", "chunks", len(secrets), "size", len(data))
	return nil
}

// writePlanToVolume writes the plan to an ephemeral volume
func (sm *StorageManager) writePlanToVolume(ctx context.Context, name, namespace, planId, suffix, uuid string, data []byte) error {
	config := sm.getStorageConfig()

	// Compress the data
	compressedData, err := utils.GzipEncode(data)
	if err != nil {
		return fmt.Errorf("failed to compress plan data: %w", err)
	}

	// Get mount path
	mountPath := "/tmp/tf-storage"
	if config.VolumeMountPath != "" {
		mountPath = config.VolumeMountPath
	}

	// Create the directory if it doesn't exist
	planDir := filepath.Join(mountPath, "plans", namespace, name)
	if err := os.MkdirAll(planDir, 0755); err != nil {
		return fmt.Errorf("failed to create plan directory: %w", err)
	}

	// Write the plan file
	filename := fmt.Sprintf("tfplan-%s-%s%s.gz", sm.terraform.WorkspaceName(), planId, suffix)
	filepath := filepath.Join(planDir, filename)

	if err := os.WriteFile(filepath, compressedData, 0644); err != nil {
		return fmt.Errorf("failed to write plan file: %w", err)
	}

	sm.log.Info("wrote plan to ephemeral volume", "path", filepath, "size", len(compressedData))

	// Create a reference secret to track the file location
	return sm.createVolumeReference(ctx, name, namespace, planId, suffix, uuid, filepath)
}

// createVolumeReference creates a small secret that references the volume-stored plan
func (sm *StorageManager) createVolumeReference(ctx context.Context, name, namespace, planId, suffix, uuid, filepath string) error {
	secretName := "tfplan-" + sm.terraform.WorkspaceName() + "-" + name + suffix
	tfplanObjectKey := types.NamespacedName{Name: secretName, Namespace: namespace}

	var tfplanSecret v1.Secret
	if err := sm.Client.Get(ctx, tfplanObjectKey, &tfplanSecret); err == nil {
		// Delete existing secret
		if err := sm.Client.Delete(ctx, &tfplanSecret); err != nil {
			return fmt.Errorf("error deleting existing plan reference: %w", err)
		}
	}

	// Create reference metadata
	reference := map[string]string{
		"storage-type": "persistentVolume",
		"file-path":    filepath,
		"plan-id":      planId,
	}

	referenceBytes, err := json.Marshal(reference)
	if err != nil {
		return fmt.Errorf("failed to marshal volume reference: %w", err)
	}

	// Create a small secret with reference information
	tfplanSecret = v1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Annotations: map[string]string{
				"storage-type":            "persistentVolume",
				SavedPlanSecretAnnotation: planId,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: infrav1.GroupVersion.Group + "/" + infrav1.GroupVersion.Version,
					Kind:       infrav1.TerraformKind,
					Name:       name,
					UID:        types.UID(uuid),
				},
			},
		},
		Type: v1.SecretTypeOpaque,
		Data: map[string][]byte{
			"reference": referenceBytes,
		},
	}

	if err := sm.Client.Create(ctx, &tfplanSecret); err != nil {
		return fmt.Errorf("error creating plan reference secret: %w", err)
	}

	return nil
}

// ReadTFPlan reads the Terraform plan from the configured storage
func (sm *StorageManager) ReadTFPlan(ctx context.Context, name, namespace, suffix string) ([]byte, error) {
	// First, try to find plan secrets using labels (chunked secrets approach)
	secretList := &v1.SecretList{}
	if err := sm.Client.List(ctx, secretList, client.InNamespace(namespace), client.MatchingLabels{
		"infra.contrib.fluxcd.io/plan-name":      name,
		"infra.contrib.fluxcd.io/plan-workspace": sm.terraform.WorkspaceName(),
	}); err == nil && len(secretList.Items) > 0 {
		// Found chunked secrets - reconstruct the plan
		p, err := plan.NewFromSecrets(name, namespace, string(sm.terraform.GetUID()), secretList.Items)
		if err != nil {
			return nil, fmt.Errorf("failed to reconstruct plan from secrets: %w", err)
		}
		return p.Bytes(), nil
	}

	// Fallback: try old single-secret format or volume reference
	secretName := "tfplan-" + sm.terraform.WorkspaceName() + "-" + name + suffix
	tfplanObjectKey := types.NamespacedName{Name: secretName, Namespace: namespace}

	var tfplanSecret v1.Secret
	if err := sm.Client.Get(ctx, tfplanObjectKey, &tfplanSecret); err != nil {
		return nil, fmt.Errorf("failed to get plan secret: %w", err)
	}

	// Check if this is a volume reference
	if storageType, exists := tfplanSecret.Annotations["storage-type"]; exists && storageType == "persistentVolume" {
		return sm.readPlanFromVolume(ctx, tfplanSecret)
	}

	// Read from secret directly (old format)
	compressedData, exists := tfplanSecret.Data[TFPlanName]
	if !exists {
		return nil, fmt.Errorf("plan data not found in secret")
	}

	// Check if data is compressed
	if encoding, exists := tfplanSecret.Annotations["encoding"]; exists && encoding == "gzip" {
		return utils.GzipDecode(compressedData)
	}

	return compressedData, nil
}

// readPlanFromVolume reads the plan data from a persistent volume
func (sm *StorageManager) readPlanFromVolume(ctx context.Context, referenceSecret v1.Secret) ([]byte, error) {
	referenceData, exists := referenceSecret.Data["reference"]
	if !exists {
		return nil, fmt.Errorf("volume reference data not found")
	}

	var reference map[string]string
	if err := json.Unmarshal(referenceData, &reference); err != nil {
		return nil, fmt.Errorf("failed to unmarshal volume reference: %w", err)
	}

	filepath, exists := reference["file-path"]
	if !exists {
		return nil, fmt.Errorf("file path not found in volume reference")
	}

	// Read the compressed file
	compressedData, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read plan file from volume: %w", err)
	}

	// Decompress the data
	return utils.GzipDecode(compressedData)
}
