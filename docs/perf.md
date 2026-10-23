# Vorschau-Leistung (Preview performance)

What the preview guarantees and what it does not.

## Guarantees (enforced by design and tests)

- No decode, render, colour conversion, scaling or image allocation on the UI thread. The UI thread swaps
  one `*image.RGBA` of exactly the widget's texture size into the viewer (fyne then uploads it without a CPU
  scale or conversion) and releases the previous frame.
- Playhead and audio never wait for a picture. The viewer shows the newest ready frame, late frames are
  dropped, there is no backlog (one request slot, one ready slot, one render in flight).
- Paused or idle shows full quality 150 ms after the last request.
- No unbounded queues: decoders are capped, frame buffers come from bounded pools.

## Best effort (reported, not promised)

- Reaching the project frame rate for several 4K layers with grade on weak machines (the preview then runs at
  half or quarter size, and as a last resort at half frame rate; the chip "Wiedergabe überlastet" shows it).
- The first frame of an 80 Mbit 4K camera file needs about 450 ms (decoder start and GOP walk). Proxies,
  hardware decoding and prewarming reduce it, they do not remove it. Every change of the preview size starts
  new decoders; the quality controller warms the decoders of the next higher step before it steps up.
- Reverse and fast play on long-GOP originals without a proxy above 2x is keyframe-only ("Nur Keyframes").
- Hardware decoding depends on driver, codec and pixel format (4:2:2 and 10-bit usually fall back to software).
- OpenGL texture upload and the redraw of the whole canvas per picture are not measurable headless. The
  LoadGovernor (kit.Do latency probe every 100 ms while playing) lowers the quality and the timeline/scope update
  rate when the UI thread is congested.

## Regression harness

Everything under `src/perf` is skipped unless `FS_PERF=1`. Run politely:

    FS_PERF=1 GOMAXPROCS=4 nice -n 19 taskset -c 0-7 go test ./src/perf -run TestBudget -count=3 -v

- `sh src/perf/genmedia.sh /tmp/fsprof` creates the test media (delete the directory afterwards).
- `FS_PERF_SLACK=2.5` loosens absolute limits on loaded machines, `FS_PERF_REPORT_ONLY=1` prints without failing.
- `TestBudget*` are the acceptance gates (UI busy, kit.Do latency, shown fps, playhead drift, GC pauses,
  allocation per frame). `TestPerfViewer*`, `TestPerfScrub*`, `TestPerfShuttle*`, `TestPerfBackground*`
  print the detailed numbers of the real viewer on real media.

## Real GL timing (manual, by the user only)

Agents must never start the application. To measure the part that cannot be measured headless (texture upload,
whole-canvas redraw), the user can start the app normally, play a 4K project in a large window and compare the
statistics overlay (View > Wiedergabe-Statistik: "UI x ms" is the kit.Do latency p95) with the numbers above.
A per-frame canvas draw timer (`FS_PERF_LOG`) has not been implemented.
