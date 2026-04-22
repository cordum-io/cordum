"""Python conformance harness for the Cordum SDK test suite.

The public surface is the `__main__` module invoked as
``python -m conformance --fixtures <dir> --sim-bin <path>``. Internals
(_diff, _operation_map, driver, _report) are private — they must keep
byte-compatible grading with the Go and TypeScript harnesses (step 9
parity test).
"""
