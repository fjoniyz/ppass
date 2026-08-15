# Private PaaS Project Status & Codebase Analysis

This project is a custom **Private PaaS (Platform as a Service)** written in Go. Its goal is to provide a lightweight system for deploying isolated services and databases on a home lab node. Instead of using Docker or Kubernetes, it utilizes low-level Linux primitives (namespaces, cgroups, virtual ethernet interfaces, and bridges) for workload isolation and traffic routing.

## 📁 Repository Structure

*   [main.go](file:///home/d0sta/private_paas/main.go): The entrypoint that defines the Cobra CLI with `create`, `delete`, and `cleanup` commands. It interacts with Redis to track deployed processes.
*   [cmd/network_manager.go](file:///home/d0sta/private_paas/cmd/network_manager.go): Implements the lower-level Linux network orchestration (creating the `br0` bridge, managing virtual ethernet (`veth`) pairs, switching interfaces between namespaces, and writing routing table policies).
*   [parser/](file:///home/d0sta/private_paas/parser/):
    *   [service.go](file:///home/d0sta/private_paas/parser/service.go) / [service_parser.go](file:///home/d0sta/private_paas/parser/service_parser.go): Parses YAML declarations for services, executes them as isolated Python subprocesses with `CLONE_NEWNET`, setups resource restrictions using cgroups, and links them to the software bridge.
    *   [db.go](file:///home/d0sta/private_paas/parser/db.go) / [db_parser.go](file:///home/d0sta/private_paas/parser/db_parser.go): Manages Postgres database configurations, invoking databases in separate namespaces via `ip netns exec`.
    *   [lb_parser.go](file:///home/d0sta/private_paas/parser/lb_parser.go): Dynamically templates Envoy routing directives and handles Envoy server reload/restart actions.
*   [config_files/python/](file:///home/d0sta/private_paas/config_files/python/): Contains testing workloads like `dummy_service.py` (port 9999), `second_service.py` (port 8888), and YAML definition templates.
*   [utils/network.go](file:///home/d0sta/private_paas/utils/network.go): A helper utility to locate the PID of the Envoy server.
*   [scripts/clean_interfaces.sh](file:///home/d0sta/private_paas/scripts/clean_interfaces.sh): A cleanup utility script to purge virtual ethernet interfaces with the `v-` prefix.

---

## 🛠️ Current Status & Architectural Evaluation

### 1. Networking Setup & Gaps vs. [plan.md](file:///home/d0sta/private_paas/plan.md)
The project's active refactoring goals listed in `plan.md` are:
1. **Envoy isolation:** Envoy should run in its own namespace, get a network interface, and attach to the bridge.
2. **Workload connection:** Each workload process must get a network interface and connect to the bridge.

**Current implementation state:**
*   **Envoy is isolated:** Envoy is now launched inside its own network namespace via the `syscall.CLONE_NEWNET` flag and attached to the `br0` bridge with the IP `10.0.0.1/24`. Both items on the plan have been successfully resolved.
*   **IP Allocation Collisions (Remaining Issue):** The system still uses static placeholder IPs:
    *   Services are hardcoded to use `10.0.0.2/24` (see [service.go:L104](file:///home/d0sta/private_paas/parser/service.go#L104)).
    *   Databases are hardcoded to use `10.0.0.3/24` (see [db.go:L24](file:///home/d0sta/private_paas/parser/db.go#L24)).
    Deploying a second service or database will cause IP address conflicts on the bridge.

### 2. Isolation Mechanism & Cgroups
*   **Services:** Isolated using Go's `syscall.CLONE_NEWNET` namespace flag during subprocess execution. They are constrained using cgroups v2 (`/sys/fs/cgroup/test`) with a process-limit constraint (`pids.max = 10`).
*   **Databases:** Placed in their own network namespace using shell-out command executions (`ip netns add/exec`). Cgroups are defined per database name with limits on memory (`memory.max = 200M`) and IO weight (`io.weight = 100`).
*   **Filesystem Isolation:** Although [docs/Architecture.md](file:///home/d0sta/private_paas/docs/Architecture.md) mentions isolating the filesystem and using Alpine minirootfs via `chroot`, there is currently no `chroot` or filesystem namespace isolation logic implemented in the codebase.

---

## 🚀 Recommended Next Steps

1.  **Dynamic IP Address Allocation:** Replace the hardcoded `10.0.0.2/24` and `10.0.0.3/24` IPs with a dynamic allocator (e.g., querying a pool or scanning the `10.0.0.0/24` subnet for unused IPs) to support multiple concurrent workloads.
