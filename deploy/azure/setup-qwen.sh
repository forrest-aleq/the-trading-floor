#!/usr/bin/env bash
# setup-qwen.sh — Provision Azure GPU VMs for self-hosted Qwen inference.
# Deploys Qwen 3.5 7B (speed tier) and Qwen 3.5 72B (analysis tier) using vLLM.
#
# Prerequisites:
#   - Azure CLI (az) installed and logged in
#   - Sufficient GPU quota in target region
#
# Usage:
#   ./setup-qwen.sh [--region eastus2] [--rg trading-floor-ai]

set -euo pipefail

# --- Configuration ---
REGION="${AZURE_REGION:-eastus2}"
RESOURCE_GROUP="${AZURE_RG:-trading-floor-ai}"
VNET_NAME="tf-vnet"
SUBNET_NAME="gpu-subnet"
NSG_NAME="gpu-nsg"

# VM configurations
SPEED_VM_NAME="qwen-7b-speed"
SPEED_VM_SIZE="Standard_NC24ads_A100_v4"  # 1x A100 80GB — sufficient for 7B
SPEED_MODEL="Qwen/Qwen2.5-7B-Instruct"

ANALYSIS_VM_NAME="qwen-72b-analysis"
ANALYSIS_VM_SIZE="Standard_NC48ads_A100_v4"  # 2x A100 80GB — needed for 72B
ANALYSIS_MODEL="Qwen/Qwen2.5-72B-Instruct-AWQ"  # AWQ quantized to fit 2x A100

ADMIN_USER="tfadmin"
SSH_KEY_PATH="$HOME/.ssh/id_rsa.pub"

# Parse CLI args
while [[ $# -gt 0 ]]; do
    case $1 in
        --region) REGION="$2"; shift 2 ;;
        --rg) RESOURCE_GROUP="$2"; shift 2 ;;
        *) echo "Unknown arg: $1"; exit 1 ;;
    esac
done

echo "=== Trading Floor: Qwen GPU Setup ==="
echo "Region:         $REGION"
echo "Resource Group: $RESOURCE_GROUP"
echo "Speed VM:       $SPEED_VM_SIZE ($SPEED_MODEL)"
echo "Analysis VM:    $ANALYSIS_VM_SIZE ($ANALYSIS_MODEL)"
echo ""

# --- Resource Group ---
echo "[1/7] Creating resource group..."
az group create \
    --name "$RESOURCE_GROUP" \
    --location "$REGION" \
    --tags project=trading-floor component=inference \
    --output none

# --- Networking ---
echo "[2/7] Creating VNet and subnet..."
az network vnet create \
    --resource-group "$RESOURCE_GROUP" \
    --name "$VNET_NAME" \
    --address-prefix 10.0.0.0/16 \
    --subnet-name "$SUBNET_NAME" \
    --subnet-prefix 10.0.1.0/24 \
    --output none

az network nsg create \
    --resource-group "$RESOURCE_GROUP" \
    --name "$NSG_NAME" \
    --output none

# Allow SSH and vLLM API (8000) from VNet only
az network nsg rule create \
    --resource-group "$RESOURCE_GROUP" \
    --nsg-name "$NSG_NAME" \
    --name AllowSSH \
    --priority 100 \
    --source-address-prefixes 10.0.0.0/16 \
    --destination-port-ranges 22 \
    --protocol TCP \
    --output none

az network nsg rule create \
    --resource-group "$RESOURCE_GROUP" \
    --nsg-name "$NSG_NAME" \
    --name AllowVLLM \
    --priority 110 \
    --source-address-prefixes 10.0.0.0/16 \
    --destination-port-ranges 8000 \
    --protocol TCP \
    --output none

# --- Cloud-init script for GPU VMs ---
cat > /tmp/cloud-init-vllm.yaml << 'CLOUDINIT'
#cloud-config
package_update: true
packages:
  - docker.io
  - nvidia-container-toolkit

runcmd:
  # Install NVIDIA drivers
  - distribution=$(. /etc/os-release;echo $ID$VERSION_ID)
  - curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
  - apt-get update && apt-get install -y nvidia-driver-550

  # Configure Docker for NVIDIA
  - nvidia-ctk runtime configure --runtime=docker
  - systemctl restart docker

  # Pull vLLM image
  - docker pull vllm/vllm-openai:latest

  # Create systemd service (model-specific args injected via custom-data)
  - echo "GPU setup complete — vLLM service ready to start"
CLOUDINIT

# --- Speed Tier VM (Qwen 7B) ---
echo "[3/7] Creating speed-tier VM ($SPEED_VM_NAME)..."
az vm create \
    --resource-group "$RESOURCE_GROUP" \
    --name "$SPEED_VM_NAME" \
    --size "$SPEED_VM_SIZE" \
    --image "Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest" \
    --admin-username "$ADMIN_USER" \
    --ssh-key-values "$SSH_KEY_PATH" \
    --vnet-name "$VNET_NAME" \
    --subnet "$SUBNET_NAME" \
    --nsg "$NSG_NAME" \
    --os-disk-size-gb 256 \
    --custom-data /tmp/cloud-init-vllm.yaml \
    --tags tier=speed model=qwen-7b \
    --output none

SPEED_IP=$(az vm show \
    --resource-group "$RESOURCE_GROUP" \
    --name "$SPEED_VM_NAME" \
    -d --query privateIps -o tsv)

echo "  Speed VM private IP: $SPEED_IP"

# --- Analysis Tier VM (Qwen 72B) ---
echo "[4/7] Creating analysis-tier VM ($ANALYSIS_VM_NAME)..."
az vm create \
    --resource-group "$RESOURCE_GROUP" \
    --name "$ANALYSIS_VM_NAME" \
    --size "$ANALYSIS_VM_SIZE" \
    --image "Canonical:0001-com-ubuntu-server-jammy:22_04-lts-gen2:latest" \
    --admin-username "$ADMIN_USER" \
    --ssh-key-values "$SSH_KEY_PATH" \
    --vnet-name "$VNET_NAME" \
    --subnet "$SUBNET_NAME" \
    --nsg "$NSG_NAME" \
    --os-disk-size-gb 512 \
    --custom-data /tmp/cloud-init-vllm.yaml \
    --tags tier=analysis model=qwen-72b \
    --output none

ANALYSIS_IP=$(az vm show \
    --resource-group "$RESOURCE_GROUP" \
    --name "$ANALYSIS_VM_NAME" \
    -d --query privateIps -o tsv)

echo "  Analysis VM private IP: $ANALYSIS_IP"

# --- Create vLLM systemd services ---
echo "[5/7] Deploying vLLM service to speed VM..."
az vm run-command invoke \
    --resource-group "$RESOURCE_GROUP" \
    --name "$SPEED_VM_NAME" \
    --command-id RunShellScript \
    --scripts "
cat > /etc/systemd/system/vllm.service << EOF
[Unit]
Description=vLLM OpenAI-compatible API (Qwen 7B Speed Tier)
After=docker.service nvidia-persistenced.service
Requires=docker.service

[Service]
Restart=always
RestartSec=10
ExecStartPre=-/usr/bin/docker stop vllm
ExecStartPre=-/usr/bin/docker rm vllm
ExecStart=/usr/bin/docker run --name vllm --gpus all -p 8000:8000 \
    -e HF_TOKEN=\${HF_TOKEN} \
    vllm/vllm-openai:latest \
    --model $SPEED_MODEL \
    --max-model-len 8192 \
    --tensor-parallel-size 1 \
    --gpu-memory-utilization 0.90 \
    --enforce-eager
ExecStop=/usr/bin/docker stop vllm

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable vllm
systemctl start vllm
" --output none

echo "[6/7] Deploying vLLM service to analysis VM..."
az vm run-command invoke \
    --resource-group "$RESOURCE_GROUP" \
    --name "$ANALYSIS_VM_NAME" \
    --command-id RunShellScript \
    --scripts "
cat > /etc/systemd/system/vllm.service << EOF
[Unit]
Description=vLLM OpenAI-compatible API (Qwen 72B Analysis Tier)
After=docker.service nvidia-persistenced.service
Requires=docker.service

[Service]
Restart=always
RestartSec=10
ExecStartPre=-/usr/bin/docker stop vllm
ExecStartPre=-/usr/bin/docker rm vllm
ExecStart=/usr/bin/docker run --name vllm --gpus all -p 8000:8000 \
    -e HF_TOKEN=\${HF_TOKEN} \
    vllm/vllm-openai:latest \
    --model $ANALYSIS_MODEL \
    --max-model-len 4096 \
    --tensor-parallel-size 2 \
    --gpu-memory-utilization 0.92 \
    --quantization awq \
    --enforce-eager
ExecStop=/usr/bin/docker stop vllm

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable vllm
systemctl start vllm
" --output none

# --- Output env vars ---
echo "[7/7] Done!"
echo ""
echo "=== Environment Variables ==="
echo "Add these to your .env or trading-floor config:"
echo ""
echo "# Self-hosted Qwen (replace OpenRouter for speed + analysis tiers)"
echo "LLM_SPEED_URL=http://$SPEED_IP:8000/v1"
echo "LLM_SPEED_MODEL=$SPEED_MODEL"
echo "LLM_ANALYSIS_URL=http://$ANALYSIS_IP:8000/v1"
echo "LLM_ANALYSIS_MODEL=$ANALYSIS_MODEL"
echo ""
echo "# Keep Claude Sonnet on OpenRouter for critical tier"
echo "LLM_CRITICAL_URL=https://openrouter.ai/api/v1"
echo "LLM_CRITICAL_MODEL=anthropic/claude-sonnet-4-20250514"
echo ""
echo "=== Estimated Costs ==="
echo "Speed tier  (NC24ads A100):   ~\$3.67/hr  (\$88/day)"
echo "Analysis tier (NC48ads A100): ~\$7.35/hr  (\$176/day)"
echo "Total:                        ~\$11/hr    (\$264/day)"
echo ""
echo "Tip: Use 'az vm deallocate' during market closed hours to save ~60%."
echo ""
echo "To tear down:"
echo "  az group delete --name $RESOURCE_GROUP --yes --no-wait"
