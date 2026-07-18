package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/pools"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
)

const maxWorkerProofPublicKeyFileBytes = 4096

type workerProofKeyFlags struct {
	keyID         *string
	algorithm     *string
	publicKeyFile *string
}

func addWorkerProofKeyFlags(fs *flagSet) workerProofKeyFlags {
	return workerProofKeyFlags{
		keyID:         fs.String("proof-key-id", "", "enrolled worker proof key ID"),
		algorithm:     fs.String("proof-algorithm", "", "proof algorithm"),
		publicKeyFile: fs.String("proof-public-key-file", "", "P-256 public key in SPKI PEM"),
	}
}

func (f workerProofKeyFlags) values() (string, string, string, error) {
	keyID := strings.TrimSpace(*f.keyID)
	algorithm := strings.TrimSpace(*f.algorithm)
	publicKeyFile := strings.TrimSpace(*f.publicKeyFile)
	if keyID == "" && algorithm == "" && publicKeyFile == "" {
		return "", "", "", nil
	}
	if keyID == "" {
		return "", "", "", fmt.Errorf("--proof-key-id is required with proof enrollment")
	}
	if publicKeyFile == "" {
		return "", "", "", fmt.Errorf("--proof-public-key-file is required with proof enrollment")
	}
	if algorithm == "" {
		algorithm = workercredentials.ProofAlgorithmECDSAP256SHA256
	}
	if algorithm != workercredentials.ProofAlgorithmECDSAP256SHA256 {
		return "", "", "", fmt.Errorf("--proof-algorithm must be %s", workercredentials.ProofAlgorithmECDSAP256SHA256)
	}
	publicKeyPEM, err := readWorkerProofPublicKey(publicKeyFile)
	if err != nil {
		return "", "", "", err
	}
	return keyID, algorithm, publicKeyPEM, nil
}

func readWorkerProofPublicKey(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read proof public key file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxWorkerProofPublicKeyFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read proof public key file: %w", err)
	}
	if len(data) > maxWorkerProofPublicKeyFileBytes {
		return "", fmt.Errorf("proof public key file too large (max %d bytes)", maxWorkerProofPublicKeyFileBytes)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", fmt.Errorf("proof public key file is empty")
	}
	return string(data), nil
}

func workerProofKeySummary(item workerCredentialRecord) string {
	if strings.TrimSpace(item.ProofKeyID) == "" {
		return "-"
	}
	if strings.TrimSpace(item.ProofAlgorithm) == "" {
		return item.ProofKeyID
	}
	return item.ProofKeyID + " (" + item.ProofAlgorithm + ")"
}

func validateWorkerCredentialAccessFlags(poolsValue, topicsValue string) ([]string, []string, error) {
	poolsList := splitComma(poolsValue)
	for _, poolName := range poolsList {
		if err := pools.ValidatePoolName(poolName); err != nil {
			return nil, nil, err
		}
	}
	topicsList := splitComma(topicsValue)
	for _, topic := range topicsList {
		if err := pools.ValidateTopicName(topic); err != nil {
			return nil, nil, err
		}
	}
	return poolsList, topicsList, nil
}
