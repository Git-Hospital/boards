Recordings of the real `istioctl` 1.24.1 in file mode, no cluster needed:

    istioctl analyze --use-kube=false -n demo -o json in/<name>.yaml

`rec/<name>.stdout`, `.stderr` and `.exit` are what it printed on each stream
and its exit code, unedited. The inputs are in `in/`.

| name | what it shows |
| --- | --- |
| clean | `[]` on stdout, the "No validation issues found" line on stderr, exit 0 |
| ist0101 | one IST0101 Error on `VirtualService demo/payments` (subset v3 not defined) |
| two-on-one | three messages on that VirtualService: IST0101 twice (Error) and IST0132 (Warning) |
| envoyfilter | three Warnings on `EnvoyFilter demo/rate-limit`: IST0133 twice, IST0151 |
| missing | a nonexistent input file: empty stdout, error on stderr, exit 1 |

In JSON mode istioctl exits 0 even when it reports Errors; exit 79 is what
text mode uses and the adapter accepts both. No Info-level message could be
produced in file mode, so the Info test uses a hand-written element.
