# Private PaaS (`ppass`)

> This document and parts of the code in this repository were done via the help of AI.

A lightweight, custom **Platform-as-a-Service (PaaS)** built in Go for Linux environments.

Instead of relying on heavy container runtimes like Docker, containerd, or Kubernetes, `ppass` orchestrates workloads using **native Linux kernel primitives**:
- **Mount Namespaces & Filesystem Isolation (`CLONE_NEWNS`, `pivot_root`):** Workloads are jailed inside an isolated Alpine Linux `rootfs` with its own Python runtime, completely separated from the host filesystem.
- **PID Namespaces (`CLONE_NEWPID`):** Workloads run in an isolated process tree with a dedicated `/proc` mount where the application executes as PID 1.
- **Network Namespaces (`CLONE_NEWNET`):** Complete network stack isolation per workload and for the Envoy proxy.
- **Virtual Ethernet (`veth`) Pairs & Linux Bridge (`br0`):** Virtual patch cables and a software bridge (`10.0.0.254/24`) for inter-service and proxy routing.
- **In-Namespace Firewalling (`iptables`):** Workload namespaces drop all external traffic except loopback and bridge subnet requests.
- **cgroups v2:** Hard limits on memory (`memory.max`), CPU quota (`cpu.max`), and thread/process count (`pids.max` for fork-bomb protection).
- **Shared IPAM (IP Address Management):** Backed by Redis for persistent, conflict-free IP assignment across CLI invocations (`10.0.0.0/24`).
- **Isolated Envoy Ingress Gateway:** Dynamic domain-based reverse proxying (`second.local`, `api.local`) generated from Go templates and isolated in its own network namespace on `10.0.0.1`.
- **Native Database Orchestration:** PostgreSQL clusters initialized with `initdb`, executed under non-root system credentials, and attached to the bridge network.

---

## 🏗️ Architecture & Topology

```
+-----------------------------------------------------------------------------------------+
|                                    Linux Host                                           |
|                                                                                         |
|  +-----------------------------------------------------------------------------------+  |
|  |                             Linux Bridge: br0 (10.0.0.254/24)                     |  |
|  +--------+----------------------------+-----------------------------+---------------+  |
|           | (v-envoy_b)                | (v-second_b)                | (v-api_b)        |
|           |                            |                             |                  |
|           | (v-envoy_n)                | (v-second_n)                | (v-api_n)        |
|  +--------v-------------------+ +------v--------------------+ +------v---------------+  |
|  | NetNS: Envoy Proxy         | | Namespaces: second-service| | Namespaces: api-svc   |  |
|  | IP: 10.0.0.1/24            | | IP: 10.0.0.2/24           | | IP: 10.0.0.3/24       |  |
|  | Ingress Listeners:         | | NetNS: CLONE_NEWNET       | | NetNS: CLONE_NEWNET   |  |
|  |  - 8081 -> second.local    | | MountNS: Alpine rootfs    | | MountNS: Alpine rootfs|  |
|  |  - 8082 -> api.local       | | PIDNS: PID 1 in /proc     | | PIDNS: PID 1 in /proc |  |
|  | cgroup: /sys/fs/cgroup     | | cgroup: /sys/fs/cgroup/PID| | cgroup: /sys/fs/cgroup|  |
|  +----------------------------+ +---------------------------+ +-----------------------+  |
|                                                                                         |
|  State Management & IPAM: Redis (localhost:6379)                                        |
+-----------------------------------------------------------------------------------------+
```

---

## ⚙️ Core Functionalities

### 1. Process, Filesystem & Namespace Isolation

`ppass` isolates workloads across three core kernel dimensions:

- **The Go Self-Exec Pattern (`/proc/self/exe`):** Because Go's multi-threaded runtime prohibits running arbitrary Go code between `fork` and `exec`, the parent CLI launches `/proc/self/exe` with `_PAAS_CONTAINER_INIT=1` under `CLONE_NEWNET | CLONE_NEWNS | CLONE_NEWPID`. The child process intercepts this in `main.go` and performs namespace setup before calling `syscall.Exec`.
- **Filesystem Isolation (`pivot_root` & `rootfs`):** 
  1. The workload script directory is bind-mounted to `/app` inside the standalone Alpine `rootfs`.
  2. The container root is swapped using `syscall.PivotRoot(rootfs, putold)`.
  3. The old host root (`/.oldroot`) is unmounted with `MNT_DETACH` and removed, air-gapping the container from host files.
- **Process Isolation (`CLONE_NEWPID`):** A private `procfs` is mounted at `/proc`, isolating the container's process table so the application runs as PID 1 and cannot see or signal host processes.
- **Network Isolation (`CLONE_NEWNET`):** Creates an independent network stack containing only an inactive loopback interface.
- **Databases (PostgreSQL):** Bootstrapped with `initdb`, executed under non-root credentials (`syscall.Credential{Uid, Gid}`), and isolated in a dedicated network namespace.

### 2. Network Plumbing & Bridge Routing

When a workload starts, `ppass` links it to the host network bridge `br0`:

1. **Veth Pair Generation:** Creates a virtual ethernet pair with truncated safe interface names (respecting the 15-character `IFNAMSIZ` limit):
   - `v-<name>_<pid>_n` (inside namespace)
   - `v-<name>_<pid>_b` (bridge attachment)
2. **Namespace Injection:** Opens `/proc/<pid>/ns/net` and moves the `_n` interface into the child process's network namespace using `netlink.LinkSetNsFd`.
3. **Bridge Attachment:** Attaches the `_b` interface to `br0` (`netlink.LinkSetMaster`) and sets it `UP`.
4. **Namespace Configuration (with OS Thread Locking):** Calls `runtime.LockOSThread()` to prevent goroutine thread migration, enters the child namespace via `netns.Set`, assigns the allocated IP address (`netlink.AddrAdd`), sets the interface `UP`, and brings up the loopback interface (`lo`).
5. **In-Namespace Firewalling (`iptables`):** Injects firewall rules inside the container namespace to allow `lo`, accept established connections, allow traffic from `10.0.0.0/24`, and drop all other ingress packets.

### 3. Shared IP Address Management (IPAM)

IP addresses are managed across CLI commands using a **Redis-backed IPAM engine** (`github.com/metal-stack/go-ipam`):

- **Subnet Pool:** `10.0.0.0/24`
- **Host Bridge (`br0`):** `10.0.0.254`
- **Envoy Gateway:** `10.0.0.1` (reserved and persistent across reloads)
- **Workload Pool:** `10.0.0.2` – `10.0.0.253` dynamically allocated to services and databases
- **Persistence & Deallocation:** When a workload is deleted via `./paas delete`, its assigned IP is automatically released back to the IPAM pool in Redis.
- **Fallback:** If Redis is temporarily unreachable (such as in offline testing), IPAM falls back gracefully to in-memory allocation.

### 4. Resource Limitations via cgroups v2

Each workload process is enrolled in a dedicated cgroup v2 hierarchy (`/sys/fs/cgroup/<pid>` or `/sys/fs/cgroup/envoyLb`):

- **Memory Limit:** Configured via `memory.max` (e.g., `128M`, `200M`). Triggers kernel OOM kill on breach.
- **CPU Quota:** Configured via `cpu.max` (e.g., `50000 100000` for 50% CPU limit).
- **Process Count (Fork-bomb protection):** Configured via `pids.max` (e.g., `15`, `20`).

### 5. Envoy Load Balancer & Ingress Routing

Envoy runs as an isolated proxy that dynamically updates whenever services are created or removed:

1. **Configuration Generation:** Each service writes its configuration fragment to `/etc/envoy/conf.d/<upstream>.yaml`.
2. **Template Rendering:** `ppass` reads all fragments and renders a combined Envoy configuration to `/run/envoy-paas.yaml` using Go templates.
3. **Domain & Port Routing:** Traffic sent to Envoy on a configured port (e.g. `8081` or `8082`) with a matching `Host` header (e.g. `second.local`, `api.local`) is reverse-proxied to the upstream workload's IP and port.

### 6. State Tracking in Redis

Active workloads are recorded in Redis under namespaced keys:
- `service:<name>`: Stores YAML metadata including `pid`, `configfilename`, and assigned `ip`.
- `database:<name>`: Stores database `pid` and assigned `ip`.

---

## 🚀 Getting Started

### Prerequisites

- **Linux OS** with cgroups v2 enabled and root/sudo permissions
- **Go 1.24+** (Go 1.26 recommended)
- **Redis Server** running on `localhost:6379`
- **Envoy Proxy** installed (via APT or Linuxbrew)
- **Alpine Rootfs** bundled in `rootfs/` with Python 3 installed

```bash
# Start Redis
sudo systemctl start redis-server # or redis-cli ping
```

---

## 🛠️ Usage

### Using the Makefile (Recommended)

| Command | Description |
| :--- | :--- |
| `make build` | Compile the `paas` binary |
| `make test` | Run the complete unit test suite |
| `make run-db` | Start `user-db` PostgreSQL database |
| `make delete-db` | Stop `user-db` database and release its IP |
| `make run-second-service` | Start `second-service` (`second.local:8081`) |
| `make test-second-service` | Test `second-service` (Envoy + direct curl) |
| `make delete-second-service`| Stop `second-service` and release its IP |
| `make run-api-service` | Start `api-service` (`api.local:8082`) |
| `make test-api-service` | Test `api-service` (Envoy + direct curl) |
| `make delete-api-service` | Stop `api-service` and release its IP |
| `make list` | Display active workloads, PIDs, and allocated IPs |
| `make logs` | Tail logs for all running workloads and Envoy |
| `make clean` | Clean binary and remove hanging virtual interfaces |

---

### Manual CLI Commands

#### 1. Create a Service or Database
```bash
sudo ./paas create config_files/python/second_service_config.yaml
sudo ./paas create config_files/python/api_service_config.yaml
```

#### 2. List Active Workloads
```bash
./paas list
```
**Output:**
```
TYPE         NAME                 PID        IP
-------------------------------------------------------
service      second-service       1029628    10.0.0.2
service      api-service          1032145    10.0.0.3
```

#### 3. Test Workloads

**Via Envoy Load Balancer:**
```bash
# Request to second-service through Envoy
curl -i -H "Host: second.local" http://10.0.0.1:8081/

# Request to api-service through Envoy
curl -i -H "Host: api.local" http://10.0.0.1:8082/
```

**Direct Connection (Namespace IP from Host):**
```bash
curl http://10.0.0.2:8888/
curl http://10.0.0.3:8888/
```

#### 4. Delete a Workload
```bash
sudo ./paas delete service second-service
sudo ./paas delete service api-service
```

---

## 📄 Configuration File Specifications

### Service Configuration Example (`config_files/python/api_service_config.yaml`)

```yaml
type: service
body: |
  name: api-service
  lb: true
  path: /home/d0sta/ppass/config_files/python/api_service.py
  technology: python
  lbconfig:
    upstreamname: api_service
    listenport: 8082
    domain: api.local
    # Optional: if servers is omitted, ppass auto-configures the dynamic IP
    servers:
      - "10.0.0.3:8888"
  limitations:
    memory: "128M"
    pids: "15"
    cpu: "50000 100000" # 50% CPU
```

### Database Configuration Example (`config_files/python/db_config.yaml`)

```yaml
type: db
body: |
  name: user-db
  technology: postgresql
  username: dbuser
  password: securepassword
  initscript: /path/to/schema.sql
```

---

## 🔍 Troubleshooting & Verification

### Inspect a Namespace
You can inspect the network interfaces, process table, and routing inside any running workload or Envoy namespace using `nsenter`:

```bash
# View network interfaces inside Envoy
sudo nsenter -t $(cat /run/envoy-paas.pid) -n ip addr

# View isolated processes inside a service namespace
sudo nsenter -t <SERVICE_PID> -p -m ps aux

# View listening sockets inside a service
sudo nsenter -t <SERVICE_PID> -n ss -tulpn
```

### Inspect cgroup v2 Resource Usage
```bash
cat /sys/fs/cgroup/<SERVICE_PID>/cgroup.procs
cat /sys/fs/cgroup/<SERVICE_PID>/memory.current
cat /sys/fs/cgroup/<SERVICE_PID>/cpu.stat
```

### Check Logs
```bash
cat /var/log/envoy-paas.log
cat /var/log/second-service.log
cat /var/log/api-service.log
```

---

## 🧪 Testing

Run all unit test suites:
```bash
make test
# or: go test -v ./...
```
