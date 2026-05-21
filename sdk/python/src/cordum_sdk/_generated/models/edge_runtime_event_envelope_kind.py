from enum import Enum


class EdgeRuntimeEventEnvelopeKind(str, Enum):
    RUNTIME_DNS_QUERY = "runtime.dns.query"
    RUNTIME_FILE_READ = "runtime.file.read"
    RUNTIME_FILE_WRITE = "runtime.file.write"
    RUNTIME_NETWORK_CONNECT = "runtime.network.connect"
    RUNTIME_PROCESS_EXEC = "runtime.process.exec"

    def __str__(self) -> str:
        return str(self.value)
