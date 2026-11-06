# Generate PDF reports in pure Go, as background jobs

Reports used to be sent as one large JSON payload and rendered in the browser, which froze it on large date ranges. Reports are now built on the backend as PDFs by a pure-Go library (embedded Thai font, no external binaries), run as background Report Jobs one at a time, reading data in batches so memory stays constant.

We rejected headless Chrome (e.g. chromedp): it would match the frontend's look more closely, but it requires Chrome on the host and breaks the single-binary tray-app deployment. The cost of pure Go is that the PDF's appearance is drawn by hand and may differ slightly from the old frontend report.
