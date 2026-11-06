# TMS Client Backend

Backend that polls temperature probes on machines, stores readings, raises alerts, and serves reports.

## Language

**Report**:
A PDF document summarising the temperature readings of selected machines and probes over a date range.
_Avoid_: Export, printout

**Report Job**:
A request to generate a Report that runs in the background and yields a downloadable PDF when finished.
_Avoid_: Task, export request

**Report File**:
The finished PDF produced by a Report Job, kept for a limited time before it is discarded.
_Avoid_: Download, attachment
