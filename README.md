# Web Terminal via SSH Gateway (PoC)

Browser-based terminal access to multiple QEMU guests through a two-process
pipeline — a browser-facing **BFF** and an infrastructure-facing **Gateway
(GW)** — with the transport (SSH today) hidden behind a swappable **Runtime**
abstraction. Full design: [DESIGN.md](DESIGN.md).

```
[ Browser / xterm.js ] --WS--> [ BFF ] --WS--> [ GW + Runtime ] --SSH--> [ vm1 / vm2 / vm3 ]
```

## 1. What this verifies

That a generic, multi-target Web Terminal can be built with the transport hidden
behind a Runtime boundary. Concretely:

- The GW launches a fleet of backend machines on startup and stops them on exit;
  targets report live status (`booting` / `ready` / `error`).
- Browser↔BFF↔GW WebSocket frames relay end-to-end unmodified (binary = terminal
  I/O, text = JSON control), so the BFF never interprets payloads.
- Selecting one of several pre-provisioned targets routes to that specific guest,
  with an interactive PTY shell, resize propagation, and concurrent sessions.
- SSH credentials stay confined to the GW configuration and the GW→target hop;
  they never reach the BFF or the browser.

## 2. Setup

Prerequisites: Go 1.25+, `qemu-system-x86_64` / `qemu-img`, and one of
`cloud-localds` / `genisoimage` / `mkisofs` / `xorriso` for the cloud-init seed.
KVM is used automatically when `/dev/kvm` is accessible (otherwise QEMU falls
back to slow TCG). On WSL, ensure your user can open `/dev/kvm` — adding it to
the `kvm` group is durable; start the GW from a fresh shell so the group applies.

1. Run the test suite:

   ```
   go test ./...
   ```

2. Build the base image, three CoW overlays, and cloud-init seeds (downloads the
   Ubuntu cloud image once):

   ```
   ./vm/fetch-image.sh
   ```

3. Start the Gateway (it launches the three guests and serves the GW endpoints).
   The startup log reports `kvm=true|false`.

   ```
   go run ./cmd/gw -config config.json -addr :8081
   ```

4. Start the BFF in a second shell:

   ```
   go run ./cmd/bff -gw http://127.0.0.1:8081 -addr :8080 -web web
   ```

5. Open <http://127.0.0.1:8080>.

Stop the GW with Ctrl-C; it terminates every QEMU guest it launched.

## 3. What to confirm in the browser

- **Target picker** — `vm1` / `vm2` / `vm3` appear with live status; targets that
  are not `ready` cannot be selected.
- **Connection** — selecting a ready target shows that guest's shell prompt
  within about 2 seconds.
- **Per-target routing** — the prompt hostname matches the selected target
  (`...@vm1` vs `...@vm3`).
- **Interactive fidelity** — `top` updates in place, `vim` opens full-screen,
  arrow keys / `Ctrl-C` / tab completion work, and 256-color output renders
  correctly, with no layout corruption.
- **Resize** — resizing the window updates `tput cols` / `tput lines` inside the
  guest and reflows a running `vim` / `top`.
- **Concurrent sessions** — two tabs on two different targets run independently
  with no cross-talk.
- **Session lifecycle** — typing `exit` or closing the tab ends the session and
  prints `[session closed]`; reconnecting to the same target succeeds.
