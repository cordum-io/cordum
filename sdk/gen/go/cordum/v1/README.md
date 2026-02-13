# Generated Protobuf Code

The canonical protobuf-generated Go files live in `core/protocol/pb/v1/`.

This directory previously held a duplicate copy. It was removed because no SDK
source files import these types — the SDK communicates with Cordum via HTTP/NATS,
not direct protobuf imports.

If future SDK packages need generated types, regenerate them here with:

```bash
make proto
```

The proto source files are at `core/protocol/proto/v1/`.
