PR review comments

1- DecodeCRDTKey subtracts 1 from the protobuf enum unconditionally. If pb is nil or pb.DataType is CRDT_DATA_TYPE_UNSPECIFIED (0), this produces a negative crdt.DataType and can misclassify keys. Consider validating pb != nil and mapping UNSPECIFIED to an error or a well-defined sentinel (and handle unknown enum values explicitly).


2- encodeORMapStringGCounter silently drops map entries when EncodeCRDTData(v) returns an error (continue). This can lead to permanent, hard-to-diagnose state loss during snapshotting / replication. Prefer returning the error to the caller (or at least surfacing it) so callers can fail the operation rather than persisting an incomplete map.

3- decodeORMapStringGCounter ignores DecodeCRDTData errors and silently skips entries. That can cause partial restores / merges with missing keys. If decode errors are expected, consider surfacing them to the caller (e.g., change signature to return (*ORMap, error)), or record an error marker so the caller can decide whether to reject the payload.

4- Save reads dataType := keyTypes[keyID] and version := versions[keyID] without checking that those maps contain the key. Missing entries will default to 0 and can result in wrong type/version being snapshotted. Consider validating presence (and returning an error) for any store key missing a corresponding type/version entry.

5- crdt.replicator.store.size and crdt.replicator.tombstone.count are modeled as Int64ObservableCounter, but these values can go up and down. In OpenTelemetry they should be observable gauges (or up/down counters), otherwise backends may treat them as monotonic sums and display incorrect results. Consider using Int64ObservableGauge for size-like instruments.

6- This example does an unchecked type assertion on the Ask reply (reply.(*crdt.GetResponse[*crdt.ORSet[string]])). If the replicator returns an unexpected message (timeout, error response, nil data, etc.), this will panic. Consider using the comma-ok form and handling unexpected replies gracefully, especially since this is meant as a reference example.

7- Unchecked type assertion msg.Data.(*crdt.ORSet[string]) can panic if Changed ever carries a different data type or Data is nil. Consider using a safe type assertion (comma-ok) and ignoring/logging unexpected payloads in this example code.

8- Store.Close deletes the snapshot DB file (os.Remove(s.path)). This makes snapshots non-durable across normal shutdowns and can also delete the snapshot on supervisor restarts (where PostStop is invoked), defeating the purpose of crash recovery. Consider separating concerns: have Close only close the DB, and a separate explicit Remove()/Cleanup() for deletion when desired.

9- Docs claim the built-in codec supports string, int, float64, bool, and protobuf types, but the current codec implementation only encodes/decodes a limited subset (e.g., ORSet[string], MVRegister[string], ORMap[string,*GCounter], GCounter, PNCounter, Flag). Either narrow this statement or extend the codec to match the documented supported types to avoid misleading users.