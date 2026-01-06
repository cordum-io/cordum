  Approval hardening (prod‑grade)

  - Record approver identity + timestamp + reason (audit trail).
  - Bind approvals to policy snapshot + job hash (prevent replay after policy changes).
  - Add expiry + revalidation on approval.
  - Enforce RBAC/tenant scoping for approve/reject endpoints.
  - Optional: quorum approvals for high‑risk topics.