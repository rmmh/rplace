A series of scripts to manipulate Reddit's r/place 2022/2023 data.

- cmd/writedelta: compress full canvas images from disk or network into delta zips
- cmd/server: serve image deltas stored in canvas zips
- cmd/eventsfromcanvas2: crunch image deltas into a binary format, and make separate files for serving on the web.
- web: 2022 frontend
- web2: 2023 frontend
