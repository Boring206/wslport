# Changelog

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
