# Design Doc: Web Terminal via SSH Gateway

## 1. Background and Objective

To provide browser-based terminal access to various virtualization platforms (Proxmox, OpenShift, Kata Containers, or public cloud VMs) in the future, a platform-agnostic architecture is required. By abstracting the underlying infrastructure behind a **Runtime** boundary and adopting **SSH** as the first transport, dependencies on any specific orchestrator API are eliminated.

This Proof of Concept (PoC) builds a two-process pipeline — a browser-facing **BFF** and an infrastructure-facing **Gateway (GW)** — that bridges a browser to multiple selectable backend machines, verifying the feasibility of a generic, multi-target Web Terminal architecture.

**Objectives:**
- Validate a platform-independent terminal connection architecture in which the transport (SSH today) is hidden behind a swappable Runtime abstraction.
- Verify bidirectional relaying between WebSocket streams and interactive backend sessions across a BFF→GW process boundary.
- Verify selection among **multiple** pre-provisioned targets from the browser, with backend lifecycle owned by the GW.
- Confirm End-to-End operation against three local QEMU guests.

## 2. Architecture Overview

The pipeline is split into two processes so that the browser-facing concern (BFF) and the infrastructure-facing concern (GW + Runtime) evolve independently. The GW owns the Runtime abstraction and the lifecycle of the backend machines.

```
[ Browser ] (xterm.js)
    │
    │ WebSocket  (text frame = JSON control / binary frame = raw terminal I/O)
    ▼
[ BFF ] (Go)
    │   - Serves static frontend assets
    │   - Proxies the target list from the GW
    │   - Relays WebSocket frames 1:1 between browser and GW (near-transparent)
    │
    │ WebSocket  (identical framing, relayed end-to-end)
    ▼
[ GW ] (Go / Runtime owner)
    │   - On startup: launches all configured backend machines (EnsureStarted)
    │   - Polls readiness and exposes per-target status
    │   - Attaches an interactive session per request and bridges it to the WebSocket
    │   - On shutdown: terminates all launched backends
    │   - Holds all credentials (never sent toward the browser)
    │
    │ Runtime abstraction  (QEMU implementation → SSH today; other runtimes later)
    ▼
[ Targets ]  vm1 / vm2 / vm3   (Ubuntu guests, each running sshd)
```

The transport (SSH) lives entirely inside the QEMU Runtime implementation. A future Kata / Kubernetes / cloud Runtime can satisfy the same Runtime and Session contract without changing the BFF or the wire protocol.

## 3. Technology Stack

- **Frontend:** Vanilla JS
  - **Terminal Emulator:** `xterm.js` with the fit addon (window-size → PTY-size reconciliation)
- **BFF / GW:** Go (single module, two commands: `cmd/bff`, `cmd/gw`)
  - **WebSocket Handling:** `github.com/gorilla/websocket`
  - **SSH Protocol Control:** `golang.org/x/crypto/ssh`
- **Target Environment (validation):** Three QEMU guests booted from an Ubuntu cloud image, provisioned via cloud-init, reachable on the host loopback via QEMU user-mode networking port forwards.

## 4. Component Responsibilities

**BFF (browser-facing):**
- Serves the static frontend.
- Exposes the target list to the browser by proxying the GW (status included; no credentials).
- Accepts the browser WebSocket, opens a WebSocket to the GW for the chosen target, and relays frames in both directions without interpreting payloads.

**GW (infrastructure-facing):**
- Owns the Runtime. On startup it launches every configured target and begins readiness polling.
- Exposes the target list with live status (`booting` / `ready` / `error`).
- On an attach request it asks the Runtime for an interactive Session and bridges that Session to the WebSocket: binary frames map to session I/O, control frames drive resize.
- On termination it instructs the Runtime to stop every launched target.
- Holds all credentials in its configuration; credentials never cross toward the BFF or the browser.

**Runtime abstraction (inside the GW):** transport-neutral contract with two responsibilities — lifecycle and session.

| Method | Responsibility |
|---|---|
| `Targets()` | Return the configured targets with current status. |
| `EnsureStarted()` | Launch all targets; begin readiness checks. |
| `Attach(id)` | Open an interactive session to one target and return a `Session`. |
| `Shutdown()` | Stop every launched target and release resources. |

The returned **Session** is transport-neutral: a bidirectional byte stream plus a `Resize(cols, rows)` operation. The QEMU implementation realizes a Session as an SSH connection with an allocated PTY; other runtimes may realize it differently while presenting the same contract.

## 5. Wire Protocol

A single framing convention is used on both WebSocket hops (browser↔BFF and BFF↔GW) so frames pass end-to-end unmodified:

| WebSocket frame type | Meaning | Direction |
|---|---|---|
| Binary | Raw terminal bytes (keystrokes one way, shell output the other) | Both |
| Text | JSON control message | Browser → backend |

Control messages carry, at minimum, a `resize` message with `cols` and `rows`. The first control message is sent immediately after the session opens so the remote PTY matches the browser viewport from the start.

## 6. Data Flow Design

- **Target listing:** The browser requests the target list from the BFF, which proxies the GW. Each entry exposes an opaque `id`, a human `label`, and a `status`. Targets that are not `ready` are presented as non-selectable.
- **Session establishment:** Selecting a ready target opens a browser WebSocket carrying the chosen `id`. The BFF opens a corresponding WebSocket to the GW. The GW calls `Attach(id)`; the QEMU Runtime dials that target's `sshd`, authenticates, requests a PTY, and starts the shell.
- **Input flow:** Keystrokes from `xterm.js` are sent as binary frames, relayed by the BFF, and written to the session by the GW.
- **Output flow:** The GW reads session output and pushes it as binary frames; the BFF relays them; `xterm.js` renders them.
- **Resize flow:** A browser viewport change emits a `resize` control frame; the GW applies it to the live session, reflowing the remote PTY.
- **Teardown:** Closing the browser tab, or the remote shell exiting, closes both WebSocket hops and the underlying session.

## 7. VM Provisioning

- **Image:** One Ubuntu cloud image is downloaded once as a read-only base. Three copy-on-write overlays are derived from it, so per-target disk and download cost stay minimal.
- **Identity:** cloud-init sets distinct hostnames (`vm1`, `vm2`, `vm3`) so the target reached is visible in the shell prompt, and enables password authentication with a shared PoC user/password.
- **Networking:** QEMU user-mode networking forwards host loopback ports `2222`, `2223`, `2224` to each guest's port 22. No elevated privileges are required for networking; KVM acceleration is enabled automatically when `/dev/kvm` is available.
- **Lifecycle ownership:** The GW launches and stops the guests. Operators start the GW process; the GW is responsible for the QEMU processes beneath it.

## 8. Configuration

The GW reads a single configuration source describing the selected runtime and its targets. Each target entry provides an `id`, a `label`, a connection endpoint (host/port), and credentials. Credentials reside only in the GW configuration and never propagate outward. The BFF is configured only with the GW address.

## 9. Scope Definition

This PoC focuses on a generic, multi-target terminal proxy with a swappable runtime.

**In-Scope:**
- A two-process BFF + GW pipeline with end-to-end WebSocket relaying.
- A Runtime abstraction with a working QEMU implementation, including target startup on GW launch and teardown on GW exit.
- Static multi-target selection from the browser (three pre-provisioned targets), with credentials confined to the GW.
- Password authentication from the GW to each target; host-key verification is intentionally disabled for the PoC.
- PTY allocation, shell execution, resize propagation, and interactive CUI rendering (e.g. `top`, `vim`).

**Out-of-Scope:**
- **Dynamic Target Resolution:** the target set and credentials are static configuration, not resolved per-user from a database or token.
- **Browser-side authentication / authorization.**
- **Multi-hop SSH (bastion), port forwarding, and X11 forwarding.**
- **Non-QEMU Runtime implementations** (the abstraction is provided; additional implementations are future work).

## 10. Success Criteria

Each criterion is a concrete, observable check.

1. **GW startup launches the fleet.** Running the GW results in exactly three QEMU processes. Within 60 seconds of GW start, all three targets transition to `ready` as reported by the target-list endpoint.
2. **Clean teardown.** Sending SIGINT to the GW terminates all three QEMU processes, leaving no orphaned `qemu` processes on the host.
3. **Target selection surface.** The browser at the BFF address presents a picker listing `vm1`, `vm2`, and `vm3` with live status; targets not yet `ready` cannot be selected.
4. **Connection establishment.** Selecting a `ready` target displays that guest's interactive shell prompt in the browser terminal within about 2 seconds of selection.
5. **Correct per-target routing.** Connecting to `vm1` versus `vm3` yields prompts whose hostnames match the selected target (`...@vm1` vs `...@vm3`), demonstrating routing rather than a single fixed backend.
6. **Interactive fidelity.** Inside a session: `top` updates in place without scrollback flooding; `vim` opens full-screen with correct cursor placement; arrow keys, `Ctrl-C`, and tab completion behave as on a native terminal; 256-color output renders correctly. No layout corruption is observed.
7. **Resize propagation.** After resizing the browser window, `tput cols` and `tput lines` inside the guest report the new dimensions, and a running `vim` or `top` redraws to fill the new size without corruption.
8. **Concurrent independent sessions.** Two browser tabs connected to two different targets run simultaneously with independent input/output and no cross-talk.
9. **Credential confinement.** SSH credentials are absent from browser↔BFF WebSocket frames (inspected via browser dev tools) and from the BFF process; they appear only in the GW configuration and on the GW↔target SSH hop.
10. **Session lifecycle.** Typing `exit` or closing the browser tab closes the SSH session and releases the GW-side session; a subsequent reconnection to the same target succeeds.

## 11. Repository Structure

```
poc/web-terminal/
├── DESIGN.md
├── go.mod                  # single Go module
├── cmd/
│   ├── gw/                 # Gateway: EnsureStarted, target list, attach (WS), SIGINT → stop fleet
│   └── bff/                # BFF: static serve, target-list proxy, browser↔GW WS relay
├── internal/
│   ├── runtime/            # Runtime + Session contract and the QEMU implementation
│   └── wire/               # shared framing constants (text = control / binary = data)
├── config.json             # GW runtime + targets + credentials (GW only)
├── web/
│   └── index.html          # target picker + xterm.js + fit addon
├── vm/
│   ├── fetch-image.sh       # download base image, build 3 overlays, build cloud-init seeds
│   └── cloud-init/          # per-target seed data (vm1 / vm2 / vm3)
└── README.md               # verification procedure
```

## 12. Verification Procedure

1. Run `./vm/fetch-image.sh` to download the base image and build the three overlays and cloud-init seeds.
2. Start the GW (it launches the three guests and serves the GW endpoints).
3. Start the BFF.
4. Open the BFF address in a browser, select a target, and exercise the success criteria above (prompt, routing, `top`/`vim`, resize, concurrent tabs).

## 13. Next Steps (Production Readiness)

- **Dynamic Routing:** resolve target endpoints and credentials per user/request (e.g. from a database keyed by a token) instead of static configuration.
- **Additional Runtimes:** implement the Runtime contract for Proxmox, Kubernetes/OpenShift, or cloud providers, validating that the BFF and wire protocol remain unchanged.
- **Audit Logging:** intercept the I/O streams transiting the GW and persist them for compliance.
- **Browser-side authentication and authorization.**
