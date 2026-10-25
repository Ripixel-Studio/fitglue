#!/bin/bash
set -e

# GCP OIDC Setup for CircleCI
# This script configures Workload Identity Federation for keyless authentication
#
# Usage: ./scripts/setup_oidc.sh <environment>
# Example: ./scripts/setup_oidc.sh test

# Validate environment argument
ENV=${1:-dev}
if [[ ! "$ENV" =~ ^(dev|test|prod)$ ]]; then
  echo "❌ Error: Invalid environment '$ENV'"
  echo "Usage: $0 <dev|test|prod>"
  exit 1
fi

PROJECT_ID="fitglue-server-${ENV}"
CIRCLECI_ORG_ID="ecdc6640-c8ad-40c7-8710-b28261eb9107"
POOL_NAME="circleci-pool"
PROVIDER_NAME="circleci-provider"
SA_NAME="circleci-deployer"

echo "🔧 Setting up OIDC for CircleCI -> GCP authentication"
echo "Environment: $ENV"
echo "Project: $PROJECT_ID"
echo ""

# Get project number dynamically
echo "📊 Fetching project number..."
PROJECT_NUMBER=$(gcloud projects describe "$PROJECT_ID" --format="value(projectNumber)")
if [ -z "$PROJECT_NUMBER" ]; then
  echo "❌ Error: Could not fetch project number for $PROJECT_ID"
  echo "Make sure the project exists and you have access to it."
  exit 1
fi
echo "Project Number: $PROJECT_NUMBER"
echo ""

# Enable required APIs
echo "🔌 Enabling required APIs..."
gcloud services enable \
  iamcredentials.googleapis.com \
  cloudresourcemanager.googleapis.com \
  iam.googleapis.com \
  cloudfunctions.googleapis.com \
  run.googleapis.com \
  cloudbuild.googleapis.com \
  artifactregistry.googleapis.com \
  eventarc.googleapis.com \
  pubsub.googleapis.com \
  firestore.googleapis.com \
  cloudscheduler.googleapis.com \
  storage.googleapis.com \
  dns.googleapis.com \
  identitytoolkit.googleapis.com \
  --project="$PROJECT_ID"
echo "APIs enabled"
echo ""

# 1. Create Workload Identity Pool
echo "📦 Creating Workload Identity Pool..."
gcloud iam workload-identity-pools create "$POOL_NAME" \
  --project="$PROJECT_ID" \
  --location="global" \
  --display-name="CircleCI OIDC Pool" \
  --description="Workload Identity Pool for CircleCI deployments" || echo "Pool already exists, continuing..."

# 2. Create OIDC Provider
echo "🔑 Creating OIDC Provider..."
gcloud iam workload-identity-pools providers create-oidc "$PROVIDER_NAME" \
  --project="$PROJECT_ID" \
  --location="global" \
  --workload-identity-pool="$POOL_NAME" \
  --display-name="CircleCI OIDC Provider" \
  --issuer-uri="https://oidc.circleci.com/org/$CIRCLECI_ORG_ID" \
  --allowed-audiences="$CIRCLECI_ORG_ID" \
  --attribute-mapping="google.subject=assertion.sub,attribute.project_id=assertion.aud" \
  --attribute-condition="assertion.aud=='$CIRCLECI_ORG_ID'" || echo "Provider already exists, continuing..."

# 3. Create Service Account
echo "👤 Creating Service Account..."
gcloud iam service-accounts create "$SA_NAME" \
  --project="$PROJECT_ID" \
  --display-name="CircleCI Deployer" \
  --description="Service account for CircleCI OIDC deployments" || echo "Service account already exists, continuing..."

# 4. Grant permissions to Service Account
echo "🔐 Granting permissions to Service Account..."

# Grant Editor role (broad permissions for most resources)
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:$SA_NAME@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/editor" \
  --condition=None

# Grant Datastore Owner (required for Firestore database creation)
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:$SA_NAME@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/datastore.owner" \
  --condition=None

# Grant Cloud Run Admin (required for setting IAM policies on Cloud Run services)
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:$SA_NAME@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/run.admin" \
  --condition=None

# Grant Project IAM Admin (required for setting project-level IAM bindings)
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:$SA_NAME@$PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/resourcemanager.projectIamAdmin" \
  --condition=None

echo "Permissions granted"

# 5. Allow CircleCI to impersonate the Service Account
echo "🎭 Configuring Workload Identity binding..."
gcloud iam service-accounts add-iam-policy-binding \
  "$SA_NAME@$PROJECT_ID.iam.gserviceaccount.com" \
  --project="$PROJECT_ID" \
  --role="roles/iam.workloadIdentityUser" \
  --member="principalSet://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/$POOL_NAME/attribute.project_id/$CIRCLECI_ORG_ID"

echo ""
echo "✅ OIDC Setup Complete!"
echo ""
echo "📋 Configuration Summary:"
echo "  Workload Identity Pool: projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/$POOL_NAME"
echo "  Provider: projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/$POOL_NAME/providers/$PROVIDER_NAME"
echo "  Service Account: $SA_NAME@$PROJECT_ID.iam.gserviceaccount.com"
echo ""
echo "🔄 Next steps:"
echo "  1. The CircleCI config has been updated to use OIDC"
echo "  2. Commit and push the changes"
echo "  3. CircleCI will automatically authenticate using OIDC tokens"
