# Unreleased

## 🔧 Fixes

- **`WithRelocationDisabled` survives shutdown and restart** ([#1349](https://github.com/Tochemey/goakt/issues/1349)). The actor teardown no longer resets the relocation and singleton flags, and stopped actors are excluded from the graceful-shutdown relocation snapshot.

- **A failed remote grain activation no longer displaces the owner** ([#1350](https://github.com/Tochemey/goakt/issues/1350)). The owner's registry entry is released only after a transport failure and once cluster membership confirms the node has left; every other error is returned to the caller. A claim made for a remote peer is rolled back when the peer rejects the activation and kept when the outcome is unknown. Every release of a registry entry is conditional on the entry still naming the expected node, with the check and the deletion serialized cluster-wide, and a failed rollback of a claim is reported instead of discarded.
