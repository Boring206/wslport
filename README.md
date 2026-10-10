# wslport

**English** | [繁體中文](README.zh-TW.md)

Find out what is holding a TCP port on Windows, even when the real owner is a process inside WSL, and
kill it once you confirm.

![Demo: npm run dev fails with EADDRINUSE on port 3000; wslport 3000 names the node process that another project's npm run dev left running inside WSL and kills it on confirmation; npm run dev then starts](docs/demo.gif)

When "port 3000 is already in use" on Windows, `netstat` and Task Manager often point at `wslrelay.exe`.
That is WSL's localhost forwarder. The real owner lives in one of your distros, and killing `wslrelay.exe`
only breaks forwarding for every WSL port. `wslport` gives you the actual answer:

```
> wslport 3000
Port 3000 → node (PID 4321) in WSL distro Ubuntu-24.04
  Address     *:3000
  Command     next-server (v15.1.0)
  Directory   /home/me/projects/my-app
  User        me
  Started     2 hr ago
  Parent      sh -c next dev (PID 4300)
              ← npm run dev (PID 4290)
              ← -bash (PID 512)
  wslrelay.exe (PID 9876) on the Windows side is only a forwarder; the real owner is inside WSL.

Kill node (PID 4321)? [y/N]
```

## Install

Requires Node.js 18 or newer. Install from a Windows terminal or from inside WSL:

```
npm install -g wslport
```

Or run it without installing:

```
npx wslport 3000
```

The package ships a prebuilt Windows executable (x64 and arm64), so Go is not needed. When installed
inside WSL, the same executable runs through WSL's Windows interop.

## Usage

```
wslport              list every listening port on Windows and in each distro
wslport <port>       show what holds this port, then offer to kill it

  -n, --no-kill      only look, never offer to kill
  -k, --kill         kill without asking (only when there is exactly one owner)
  -f, --force        kill WSL processes with SIGKILL straight away
      --lang <lang>  interface language: en or zh-TW
      --debug        show timings and probe results
  -h, --help         show help
  -v, --version      show the version
```

Exit codes: `0` an owner was found (or killed), `1` the port is not in use, `2` an error occurred or
`-k` could not kill anything.

The port can be written as `3000` or `:3000`, and options may come before or after it.

## Language

The interface is available in English and Traditional Chinese. By default it follows the Windows display
language: Traditional Chinese (Taiwan, Hong Kong, Macau) gets Chinese, everything else gets English.

To choose explicitly, pass `--lang en` or `--lang zh-TW`, or set the `WSLPORT_LANG` environment variable.

## What it tells you

- **Windows programs**: name, PID, full path, command line, working directory and start time.
- **Processes inside WSL**: which distro, name, PID, command line, working directory and user, plus the
  systemd unit when it is a service.
- **Who started it**: up to three parent processes, so you can tell that this `node` came from
  `npm run dev` in a particular terminal.
- **Docker containers**: when Docker published the port for a container, the container name and image
  are shown and killing uses `docker stop` instead.
- **Ports that fail to bind although nothing listens**: when a port falls inside a Windows reserved port
  range (Hyper-V/WinNAT), `netstat` shows nothing yet programs get "access denied". `wslport` names the
  range and prints a temporary and a permanent fix.
- **Ports borrowed by an outbound connection**: when the system has handed the port to an outgoing
  connection, it says which program owns it and leaves it alone.

## Safeguards when killing

- It always shows first and asks second. No interactive input counts as "no".
- Before killing, it compares the process start time to make sure the PID has not been reused.
- WSL processes get `SIGTERM` first; only if they are still alive after 3 seconds does it ask about
  `SIGKILL`.
- It looks the port up again afterwards. If the same program holds it again, it tells you that systemd,
  PM2 or a Windows service restarted it.
- These are shown but never killed: the Windows kernel (PID 4), critical system processes,
  `svchost.exe`, WSL's own processes and `wslrelay.exe`.
- `wslport` only inspects and kills processes. It never changes system settings; the commands that fix
  reserved port ranges are printed for you to run or not.

## How it works

1. It reads the TCP table straight from the Windows API (`GetExtendedTcpTable`) instead of parsing
   `netstat` output, so the system language does not matter.
2. In parallel, it runs a read-only probe script (`ss` plus `/proc`) in every **running** WSL2 distro,
   as root so that processes of all users are visible. Stopped distros are never started.
3. It merges both sides: when Windows shows `wslrelay.exe` and a WSL process listens on the same port,
   the WSL process is reported as the owner.

WSL is probed whatever the Windows side shows, so it also works in mirrored networking mode, where
Windows does not show an owner at all.

## Limitations

- TCP only.
- WSL1 distros are not probed; their processes appear on the Windows side directly.
- Killing services or other users' Windows processes needs an elevated terminal (Run as administrator).
  Their path, command line and working directory cannot be read either.
- The distro needs `ss` (iproute2) or `netstat`; most distros have one by default.
- Docker: Docker Engine installed inside a WSL distro is tested. Docker Desktop is handled according to
  Docker's documentation (the published port is held by `com.docker.backend.exe`) but has not been
  verified on a real Docker Desktop install yet. If the container is not shown, please open an issue
  with the `--debug` output.

## Troubleshooting

- **A WSL process is not found**: add `--debug` to see the probe result for each distro. The distro has
  to be a running WSL2 distro with `ss` or `netstat` installed.
- **Every run takes a second or two**: if the first `--debug` line, `process creation to program start`,
  accounts for most of the time, the delay happens before wslport starts running (for example an
  antivirus scanning the executable), not in the lookup. Excluding the wslport executable in the
  antivirus settings is the usual remedy. The lookup itself usually finishes within half a second,
  most of it spent calling `wsl.exe`.
- **"cannot run Windows programs" inside WSL**: Windows interop is disabled; check the `[interop]`
  section of `/etc/wsl.conf`.
- **The port is taken again right after killing**: a supervisor (systemd, PM2, a Docker restart policy)
  restarts the process. Stop it from the supervisor; wslport tells you which one.

## Questions and feedback

- Bugs, or output that looks wrong: open an [issue](https://github.com/Boring206/wslport/issues) with
  the `--debug` output.
- Questions, ideas, or notes on how you use it: start a
  [discussion](https://github.com/Boring206/wslport/discussions).

## Development

Requires Go 1.24 or newer and Node.js.

```
npm test         # go vet plus unit tests
npm run build    # builds bin/wslport-x64.exe and bin/wslport-arm64.exe
npm run e2e      # end-to-end tests: starts, queries and kills real test processes inside WSL
node bin/wslport.js 3000
```

When developing inside WSL without Go installed there, the build script falls back to `go.exe` on
Windows; set the `GO` environment variable to point somewhere else.

All interface text lives in `i18n.go`, once per language. Add or change both when you touch a message;
the tests check that nothing is missing.

## License

[MIT](LICENSE)
