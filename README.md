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
- Each guest ships with the **bloodhound** eBPF tracer baked into its cloud-init
  seed, running as a resident systemd service from first boot, scoped to the
  interactive `poc` user. (Surfacing the trace stream to the browser is a separate
  step — see `poc/traced-web-terminal`. Here it is only resident and inspectable
  from inside the guest.)

## 2. Setup

Prerequisites: Go 1.25+, `qemu-system-x86_64` / `qemu-img`, and one of
`cloud-localds` / `genisoimage` / `mkisofs` / `xorriso` for the cloud-init seed.
KVM acceleration is required: the GW exits immediately if `/dev/kvm` is not
accessible. On WSL, add your user to the `kvm` group (durable across `/dev/kvm`
re-creation) and start the GW from a fresh shell so the group applies.

`fetch-image.sh` bakes the bloodhound static binary into each guest's seed, so a
prebuilt binary must exist at `bloodhound/target/docker/bloodhound` (build it with
`(cd bloodhound && make build-docker)`, or point `BLOODHOUND_BIN` elsewhere).
cloud-init runs only once per fresh instance: if the overlays were already booted
without bloodhound, delete them (`rm vm/overlay-vm*.qcow2`) before re-running
`fetch-image.sh` so the new seed re-provisions.

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
- **Selection & clipboard** — drag to select; `Ctrl+C` copies *when text is
  selected* (and clears the selection), otherwise it reaches the shell as SIGINT
  — the same convention as VS Code's terminal, chosen because Chrome reserves
  `Ctrl+Shift+C` for DevTools. `Ctrl+V` (`Cmd+V`) pastes via xterm's native
  handler; right-click opens a Copy / Paste / Select All / Find / Clear menu.
  The browser Clipboard API only works in a secure context, so this requires
  `localhost` / `127.0.0.1` or HTTPS; over plain HTTP to a LAN IP the status
  line reports the clipboard as unavailable.
- **Search** — `Ctrl+Shift+F` (`Cmd+F`) opens an in-buffer search box with
  match highlighting and next/previous (`Enter` / `Shift+Enter`, `Esc` closes).
- **Links** — URLs in output are clickable (open in a new tab).
- **Rendering** — a WebGL renderer (with automatic fall-back to the DOM renderer
  on context loss), Unicode 11 width handling, and a 10k-line scrollback.

## 4. Confirming bloodhound is resident (from the browser terminal)

There is no separate trace channel in this PoC; the browser terminal is itself the
verification surface. Open a session to a `ready` target and run these in it (`poc`
has passwordless sudo):

1. **Service is up** — `systemctl is-active bloodhound` reports `active`;
   `systemctl status bloodhound` shows it running from boot.
2. **Process exists** — `pgrep -a bloodhound` lists the daemon with its
   `--uid <poc> --exclude-ports 22` arguments.
3. **eBPF is actually attached** (not just a running process) —
   `sudo bpftool prog show | grep -i blood` lists loaded BPF programs.
4. **Output reacts to your operation** (self-observation) — run a uniquely-named
   marker process and read the log **once** (do not `tail -f` it — see the note
   below):

   ```
   bash -c 'exec -a abyssmarker cat /etc/os-release' >/dev/null
   sleep 1; sudo grep -a abyssmarker /var/log/bloodhound.ndjson
   ```

   The `execve` (argv `abyssmarker`, file `/usr/bin/cat`) and the `openat` of
   `/etc/os-release` appear with the correct `pid` / `comm` / `auid`.

Notes:

- The log file is root-owned (systemd writes it), so reading it needs `sudo`.
- **Do not follow the log with `tail -f` from the operator session.** The
  follower runs under the operator's `auid` (`sudo` does not change `loginuid`),
  so its own `read` / `write` syscalls are traced, append to the log, wake the
  follower, and loop — measured at ~12× the idle event rate, which buries the
  real signal. Verify with one-shot reads as above; to watch live, follow from a
  uid bloodhound does not trace.
- bloodhound's 7 LSM tamper-resistance hooks need BPF LSM in the active stack,
  which stock Ubuntu does not enable by default; their attach is non-fatal (logged
  as warnings to `journalctl -u bloodhound`) and the syscall / tracepoint / TTY
  capture used above is unaffected. Enabling BPF LSM is deferred for the PoC.
- bloodhound reads a few `task_struct` fields (`loginuid` / `sessionid` / `tgid`)
  by byte offset. rustc emits no CO-RE relocations, so a compile-time offset is
  fixed to the build kernel (`6.8.0-49-generic`) and silently drops every
  task-scoped event on a drifted kernel — the daemon looks healthy but is blind
  (this bit us on the stock 24.04 `6.8.0-117-generic`, where `loginuid` sits at
  `0xca0`, +24 bytes). bloodhound now resolves these offsets at startup from the
  running kernel's BTF (manual CO-RE, abyss0-dev/bloodhound#37), so it is portable
  across BTF-bearing kernels. **Verify two things:** (a) `journalctl -u bloodhound`
  shows `Resolved task_struct offsets ... loginuid=0x...` (and *not* `falling
  back`); (b) actual `execve` / `openat` lines appear for a command you run — line
  growth alone is misleading, since PACKET/HEARTBEAT flow regardless of the offset.
  The `RUST_LOG=info` in the unit is what makes (a) — and the fallback guardrail —
  visible; env_logger otherwise defaults to ERROR and mutes both.
- Nested-pointer fields (TTY device path, fd → inode → super_block) remain
  compile-time fixed in the binary, so a few rich TTY/LSM-file fields can still be
  off on a drifted kernel; the core `execve` / `openat` / auid path above is not
  affected.
