# Changelog

## 0.1.1

No change in what the program does. It is built again with current tools, and the package says
better where to report a problem.

- The executables are built with Go 1.26.9 and `golang.org/x/sys` 0.49.0. Those of 0.1.0 were built
  with Go 1.25.5 and `golang.org/x/sys` 0.41.0, for which vulnerabilities have since been published,
  and package scanners showed a warning for wslport because of that. Go's own checker
  (`govulncheck`) finds none of them reachable from wslport's code, with the old tools as with the
  new ones.
- The README on npm now has the section on questions and feedback, with links to the issues and
  the discussions; it was added to the repository after 0.1.0 had been published. `package.json`
  gives the bug tracker in the form that package scanners recognise.
- Building needs Go 1.26 or newer.

## 0.1.0

First release.

- `wslport <port>` shows what holds a port. On the Windows side: name, path, command line, working
  directory and start time. When Windows only shows `wslrelay.exe`, it follows the port into WSL and
  shows the distro, process, command line, working directory and user.
- Up to three parent processes are listed on both sides.
- Kills on confirmation. WSL processes get `SIGTERM` first, then an offer of `SIGKILL`. The start time is
  compared before killing so a reused PID is never hit, and the port is re-checked afterwards, pointing
  out a supervisor restart or a child process that still holds the port.
- `wslport` with no arguments lists listening ports on Windows and in every running WSL2 distro.
- Explains ports that fail to bind because of Windows reserved port ranges (Hyper-V/WinNAT), and ports
  borrowed by an outbound connection.
- When Docker published the port, the container is shown and `docker stop` is used.
- Interface in English and Traditional Chinese, following the Windows display language; override with
  `--lang` or `WSLPORT_LANG`.
- Distributed as an npm package that installs under Node on Windows and inside WSL.
