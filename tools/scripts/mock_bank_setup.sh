#!/bin/bash
# Mock Bank Demo Setup — creates workflows, submits jobs
set -e

BASE="http://localhost:8082/api/v1"
API_KEY="17852da6e8545660dc45cfd37992308cc59ac0917066b7615b8075ad6d5d52b8"

api() {
  local method="$1" path="$2" data="$3"
  if [ -n "$data" ]; then
    curl -s -X "$method" \
      -H "X-Api-Key: $API_KEY" \
      -H "X-Tenant-ID: default" \
      -H "X-Principal-Id: admin" \
      -H "X-Principal-Role: admin" \
      -H "Content-Type: application/json" \
      -d "$data" \
      "${BASE}${path}"
  else
    curl -s -X "$method" \
      -H "X-Api-Key: $API_KEY" \
      -H "X-Tenant-ID: default" \
      -H "X-Principal-Id: admin" \
      -H "X-Principal-Role: admin" \
      "${BASE}${path}"
  fi
}

echo "=== System Status ==="
api GET /status | python -m json.tool

echo ""
echo "=== Creating Mock Bank Workflows ==="

# Workflow 1: Transaction Processing (steps as map, timeout_sec as int)
echo "--- Creating: Transaction Processing Workflow ---"
api POST /workflows '{
  "name": "bank-transaction-processing",
  "description": "Process bank transactions: validate, check fraud, execute transfer",
  "timeout_sec": 300,
  "steps": {
    "validate-transaction": {
      "type": "agent_task",
      "topic": "job.bank-validators.process",
      "input": {
        "prompt": "Validate transaction format, amounts, and account existence"
      },
      "timeout_sec": 30
    },
    "fraud-check": {
      "type": "agent_task",
      "topic": "job.fraud-detection.process",
      "depends_on": ["validate-transaction"],
      "input": {
        "prompt": "Analyze transaction for fraud patterns, velocity checks, and geo anomalies"
      },
      "timeout_sec": 60
    },
    "execute-transfer": {
      "type": "agent_task",
      "topic": "job.bank-executors.process",
      "depends_on": ["fraud-check"],
      "input": {
        "prompt": "Execute the bank transfer between accounts"
      },
      "timeout_sec": 30
    },
    "send-notification": {
      "type": "agent_task",
      "topic": "job.notification-service.process",
      "depends_on": ["execute-transfer"],
      "input": {
        "prompt": "Send transaction confirmation to customer"
      },
      "timeout_sec": 15
    }
  }
}' | python -m json.tool
echo ""

# Workflow 2: Account Onboarding
echo "--- Creating: Account Onboarding Workflow ---"
api POST /workflows '{
  "name": "bank-account-onboarding",
  "description": "New customer account onboarding: KYC, credit check, account creation",
  "timeout_sec": 600,
  "steps": {
    "kyc-verification": {
      "type": "agent_task",
      "topic": "job.compliance-agents.process",
      "input": {
        "prompt": "Perform KYC verification: identity, address, sanctions screening"
      },
      "timeout_sec": 120
    },
    "credit-check": {
      "type": "agent_task",
      "topic": "job.credit-agents.process",
      "depends_on": ["kyc-verification"],
      "input": {
        "prompt": "Run credit bureau check and score assessment"
      },
      "timeout_sec": 60
    },
    "risk-assessment": {
      "type": "agent_task",
      "topic": "job.risk-agents.process",
      "depends_on": ["kyc-verification", "credit-check"],
      "input": {
        "prompt": "Calculate customer risk score based on KYC and credit data"
      },
      "timeout_sec": 30
    },
    "create-account": {
      "type": "agent_task",
      "topic": "job.bank-executors.process",
      "depends_on": ["risk-assessment"],
      "input": {
        "prompt": "Create bank account with appropriate tier and limits"
      },
      "timeout_sec": 30
    },
    "welcome-notification": {
      "type": "agent_task",
      "topic": "job.notification-service.process",
      "depends_on": ["create-account"],
      "input": {
        "prompt": "Send welcome email with account details and card dispatch info"
      },
      "timeout_sec": 15
    }
  }
}' | python -m json.tool
echo ""

# Workflow 3: Loan Approval
echo "--- Creating: Loan Approval Workflow ---"
api POST /workflows '{
  "name": "bank-loan-approval",
  "description": "Loan application processing: assessment, underwriting, approval/denial",
  "timeout_sec": 900,
  "steps": {
    "application-review": {
      "type": "agent_task",
      "topic": "job.loan-agents.process",
      "input": {
        "prompt": "Review loan application for completeness and eligibility criteria"
      },
      "timeout_sec": 60
    },
    "credit-assessment": {
      "type": "agent_task",
      "topic": "job.credit-agents.process",
      "depends_on": ["application-review"],
      "input": {
        "prompt": "Deep credit assessment: debt-to-income ratio, payment history"
      },
      "timeout_sec": 90
    },
    "collateral-valuation": {
      "type": "agent_task",
      "topic": "job.valuation-agents.process",
      "depends_on": ["application-review"],
      "input": {
        "prompt": "Evaluate collateral value and loan-to-value ratio"
      },
      "timeout_sec": 120
    },
    "underwriting-decision": {
      "type": "agent_task",
      "topic": "job.underwriting-agents.process",
      "depends_on": ["credit-assessment", "collateral-valuation"],
      "input": {
        "prompt": "Make final underwriting decision based on all assessment data"
      },
      "timeout_sec": 60
    },
    "disbursement": {
      "type": "agent_task",
      "topic": "job.bank-executors.process",
      "depends_on": ["underwriting-decision"],
      "input": {
        "prompt": "Disburse approved loan amount to borrower account"
      },
      "timeout_sec": 30
    }
  }
}' | python -m json.tool
echo ""

echo "=== Listing Workflows ==="
api GET /workflows | python -m json.tool
echo ""

echo "=== Submitting Mock Bank Jobs ==="

# Transaction jobs
for i in $(seq 1 5); do
  echo "--- Transaction job $i ---"
  amount=$((RANDOM % 50000 + 100))
  src="ACC-$(printf '%04d' $((RANDOM % 9999)))"
  dst="ACC-$(printf '%04d' $((RANDOM % 9999)))"
  api POST /jobs "{
    \"type\": \"bank.transaction\",
    \"topic\": \"job.bank-validators.process\",
    \"prompt\": \"Process wire transfer of \$$amount from $src to $dst\",
    \"capabilities\": [\"transaction.validate\"],
    \"risk_tags\": [\"financial\", \"wire-transfer\"],
    \"metadata\": {
      \"amount\": $amount,
      \"currency\": \"USD\",
      \"source_account\": \"$src\",
      \"dest_account\": \"$dst\",
      \"transaction_type\": \"wire_transfer\"
    }
  }" | python -m json.tool
done

# KYC / Onboarding jobs
for i in $(seq 1 3); do
  echo "--- Onboarding job $i ---"
  api POST /jobs "{
    \"type\": \"bank.onboarding\",
    \"topic\": \"job.compliance-agents.process\",
    \"prompt\": \"Onboard new customer Customer-$i: verify identity, run KYC, open checking account\",
    \"capabilities\": [\"kyc.verify\"],
    \"risk_tags\": [\"compliance\", \"kyc\"],
    \"metadata\": {
      \"customer_name\": \"Customer-$i\",
      \"account_type\": \"checking\",
      \"country\": \"US\"
    }
  }" | python -m json.tool
done

# Loan applications
for i in $(seq 1 3); do
  echo "--- Loan application $i ---"
  loan_amount=$((RANDOM % 500000 + 10000))
  api POST /jobs "{
    \"type\": \"bank.loan\",
    \"topic\": \"job.loan-agents.process\",
    \"prompt\": \"Process loan application for \$$loan_amount mortgage loan from Applicant-$i\",
    \"capabilities\": [\"loan.review\"],
    \"risk_tags\": [\"financial\", \"lending\", \"high-value\"],
    \"metadata\": {
      \"loan_amount\": $loan_amount,
      \"loan_type\": \"mortgage\",
      \"term_years\": 30,
      \"applicant\": \"Applicant-$i\"
    }
  }" | python -m json.tool
done

# Fraud investigation jobs
for i in $(seq 1 2); do
  echo "--- Fraud investigation $i ---"
  acct="ACC-$(printf '%04d' $((RANDOM % 9999)))"
  api POST /jobs "{
    \"type\": \"bank.fraud-investigation\",
    \"topic\": \"job.fraud-detection.process\",
    \"prompt\": \"Investigate suspicious transaction pattern on account $acct\",
    \"capabilities\": [\"fraud.analyze\"],
    \"risk_tags\": [\"security\", \"fraud\", \"investigation\"],
    \"metadata\": {
      \"alert_type\": \"velocity_anomaly\",
      \"severity\": \"high\",
      \"flagged_transactions\": $((RANDOM % 10 + 3))
    }
  }" | python -m json.tool
done

echo ""
echo "=== Done! ==="
echo "Dashboard: http://localhost:8082"
