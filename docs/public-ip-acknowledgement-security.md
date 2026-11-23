# Public IP acknowledgement integration gates

This feature branch defines a **contract and in-memory test tracker only**. It is not an activation service.

## Trust boundaries

1. The backend owns subscription authorization, selected public IP, monotonic revision and attempt ID.
2. DNS Control must authenticate each Edge Agent using the existing control-plane trust mechanism. Derive node identity from the authenticated credential; never accept a client-supplied `nodeId` as proof of identity.
3. DNS Control must load the authoritative, immutable required-node snapshot for the revision. An empty snapshot is a failure, never success.
4. The verifier must independently inspect installed dataplane state for the **exact** subscription, IP, revision and attempt. A heartbeat or `applied: true` JSON field is not proof.
5. Persist the operation, required-node snapshot and verified acknowledgements in transactional storage before returning success. An in-memory tracker is not sufficient.
6. On restart, unknown or incomplete state must remain pending or require reconciliation; it must never be promoted to active.
7. Preserve the previously working IP until all required nodes have verified the new configuration. Do not revoke the old IP first.
8. An acknowledgement from an older revision or different attempt cannot complete a newer request.
9. The backend must not enable the subscription selection endpoint until an authenticated, durable end-to-end implementation and rollback tests pass.

## Implementation checklist

- [ ] Authenticated agent report endpoint and credential-to-node binding
- [ ] Durable operation and immutable required-node snapshot
- [ ] Real installed-state verifier (not a reported boolean)
- [ ] Transactional acknowledgement persistence and replay protection
- [ ] Reconciliation after agent or DNS Control restart
- [ ] Backend worker integration and safe old-IP handover
- [ ] End-to-end tests across backend, DNS Control and Edge Agent

Do not interpret passing package unit tests as proof of network activation.
