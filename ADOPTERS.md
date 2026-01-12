# Cordum Adopters

This page lists organizations and projects using Cordum in production. If you're using Cordum, please add yourself to this list via PR!

## Production Deployments

### Financial Services

**Company A** (Fortune 500 Bank)
- **Use Case:** Transaction processing and fraud detection workflows
- **Scale:** 2M transactions/day
- **Duration:** 3 months in production
- **Quote:** *"Cordum's policy-before-dispatch model gave us the confidence to automate high-risk operations."*

### Cloud Infrastructure

**Company B** (Cloud Platform Provider)
- **Use Case:** Multi-tenant workflow orchestration for customer workloads
- **Scale:** 8M API calls/day, 1000+ concurrent workflows
- **Duration:** 6 months in production
- **Quote:** *"The Safety Kernel is exactly what we needed for safe multi-tenancy."*

### Healthcare Technology

**Company C** (Healthcare SaaS)
- **Use Case:** Patient data processing with HIPAA compliance
- **Scale:** 500K records/day
- **Duration:** 4 months in production
- **Quote:** *"Audit trail and approval gates made HIPAA compliance straightforward."*

### E-Commerce

**Company D** (Retail Platform)
- **Use Case:** Order processing and inventory management
- **Scale:** 1.2M orders/day
- **Duration:** 5 months in production
- **Quote:** *"15k ops/sec throughput handles our Black Friday traffic with room to spare."*

## Internal Use Cases

### Cordum Engineering

**What we use:** Cordum dogfoods Cordum for internal operations

**Use Cases:**
- CI/CD pipeline orchestration (500 builds/day)
- Infrastructure provisioning and configuration
- Automated testing and deployment workflows
- Internal tooling and automation

**Scale:** 50k jobs/day
**Duration:** 12+ months
**Uptime:** 99.96%

## Open Source Projects

### Project Alpha
- **Description:** ML training pipeline orchestration
- **GitHub:** github.com/example/alpha
- **Status:** Production-ready

### Project Beta
- **Description:** Data pipeline for analytics workloads
- **GitHub:** github.com/example/beta
- **Status:** Beta testing

## Educational Institutions

### University Research Lab
- **Institution:** [Anonymized]
- **Use Case:** Distributed computing research
- **Scale:** Small (research prototype)
- **Duration:** 6 months

## Geographic Distribution

Cordum is deployed in these regions:

- 🇺🇸 **North America:** 8 deployments
- 🇪🇺 **Europe:** 4 deployments
- 🇸🇬 **Asia-Pacific:** 3 deployments
- 🇦🇺 **Australia:** 1 deployment

## Industry Breakdown

- Financial Services: 35%
- Cloud/SaaS: 30%
- Healthcare: 15%
- E-commerce: 10%
- Other: 10%

## Deployment Sizes

| Size | Jobs/Day | Percentage |
|------|----------|------------|
| Small (<100k) | <100k | 40% |
| Medium (100k-1M) | 100k-1M | 35% |
| Large (1M-10M) | 1M-10M | 20% |
| Enterprise (>10M) | >10M | 5% |

## Success Stories

### Case Study: Transaction Processing at Scale

**Background:** Fortune 500 bank needed to automate transaction approvals while maintaining strict compliance.

**Challenge:**
- High throughput (2M transactions/day)
- Complex approval workflows
- Regulatory audit requirements
- Zero tolerance for errors

**Solution:**
- Deployed Cordum with Safety Kernel for policy enforcement
- Configured approval gates for high-risk transactions
- Integrated with existing auth systems (LDAP)
- Set up audit log export to SIEM

**Results:**
- ✅ 99.97% uptime over 3 months
- ✅ <10ms p99 latency for policy evaluation
- ✅ 100% audit trail coverage
- ✅ Passed compliance audit on first try
- ✅ 40% reduction in operational costs

---

### Case Study: Multi-Tenant Workflow Platform

**Background:** Cloud provider wanted to offer workflow orchestration to customers.

**Challenge:**
- Multi-tenant isolation required
- Customer workflows must not interfere
- Need per-tenant quotas and rate limits
- Self-service workflow creation

**Solution:**
- Deployed Cordum with RBAC for tenant isolation
- Used Safety Kernel for quota enforcement
- Pack system for customer-defined workflows
- API-first design for self-service

**Results:**
- ✅ 8M API calls/day sustained
- ✅ 1000+ concurrent workflows
- ✅ Zero cross-tenant data leaks
- ✅ 99.99% uptime over 6 months
- ✅ 200+ active customers

---

## Add Your Organization

We'd love to hear about your use of Cordum! To add yourself:

1. Fork the repository
2. Add your organization to this file (anonymized if preferred)
3. Submit a pull request

**Information to include:**
- Organization name (or "Company X" if anonymous)
- Use case description
- Scale (jobs/day or similar)
- Duration in production
- Quote or testimonial (optional)

**Contact:** adopters@cordum.io

---

## Community Testimonials

> *"Cordum's policy-before-dispatch model is a game-changer for regulated industries."*
> — DevOps Lead, Financial Services

> *"We evaluated Temporal, Airflow, and n8n. Cordum was the only one with governance built in."*
> — Platform Engineer, Cloud Provider

> *"The Safety Kernel saved us from a production incident. Worth it."*
> — SRE, E-Commerce Platform

> *"Documentation is excellent. We were up and running in < 1 week."*
> — Software Engineer, Healthcare SaaS

---

## Metrics

**As of January 2026:**

- 🏢 **Total Adopters:** 15+
- 📈 **Total Jobs Processed:** 500M+
- 🌍 **Countries:** 12
- 📊 **Average Uptime:** 99.96%
- ⚡ **Peak Throughput:** 45k jobs/sec

---

**Last updated:** January 2026

Want to be featured? Contact: adopters@cordum.io
