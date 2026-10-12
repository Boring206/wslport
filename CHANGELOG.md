# Changelog

## 0.1.2

Corrections to what the npm launcher says inside WSL when Windows programs cannot be started.
The program itself is unchanged.

- The message of 0.1.1 was written for WSL 2 distros. In a WSL 1 distro the kernel setting it
  speaks of is missing when `/etc/wsl.conf` has `[interop] enabled=false`, and neither of the two
  remedies it names helps there. The message now has a line for WSL 1, and the README says which of
  its two entries is about which.
- When the kernel setting is in place but the program it names cannot be found, the launcher printed
  the same message, which is not true of that state. It now says what it found.

## 0.1.1

The program itself is unchanged. It is built again with current tools, the launcher that npm
installs has one new check, and the package says better where to report a problem.

- The executables are built with Go 1.26.9 and `golang.org/x/sys` 0.49.0. Those of 0.1.0 were built
  with Go 1.25.5 and `golang.org/x/sys` 0.41.0, for which vulnerabilities are published, and package
  scanners showed a warning for wslport because of that. Go's own checker (`govulncheck`) finds none
  of them reachable from wslport's code, with the old tools as with the new ones.
- Inside WSL, when the kernel setting that hands `.exe` files to Windows is missing (some distros
  remove it for every distro when they shut down) or switched off, the launcher now says so and
  prints the command that puts a missing one back. On most distros it printed the shell's complaints about the bytes of the
  executable instead.
- The README that goes to npm now has the recorded demo and the section on questions and feedback,
  with links to the issues and the discussions; both were added to the repository after 0.1.0 had
  been published. `package.json` gives the bug tracker in the form that package scanners recognise.
- Corrections in the README. A permanent fix for a reserved port range is printed when the
  machine's dynamic port range starts below the Windows default, not every time. What is seen when
  Windows interop is switched off for a distro is described as it happens. The English README has
  the note on piped output in Windows PowerShell 5.1 that only the Chinese one had.
- The Traditional Chinese README is now `README-zh-TW.md`. With two files named `README.*`, npm
  took the Chinese one for the package page of 0.1.0. It now shows the English README, which links
  to the Chinese one.
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
