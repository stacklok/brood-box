# Third-Party Licenses

## libkrunfw

libkrunfw is licensed under the **GNU General Public License v2.0 (GPL-2.0)**.

- Source: https://github.com/containers/libkrunfw
- License: https://github.com/containers/libkrunfw/blob/main/LICENSE

libkrunfw is distributed as a separate shared library (`libkrunfw.so.5` on
Linux, `libkrunfw.dylib` on macOS) that is loaded at runtime by the
`propolis-runner` subprocess. It is not statically linked into any binary
in this project.

When distributed in binary form (embedded in the apiary binary), the
corresponding GPL-2.0 source code is available from the upstream repository
at the URL above.
