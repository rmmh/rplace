import io
import collections
import glob
import pickle
import os
import zipfile
import zlib

import PIL, PIL.Image
import numpy as np


def nonzero_pixels(data):
    image = PIL.Image.open(io.BytesIO(data))
    array = np.array(image)
    return np.where(array!=0)


class PixelAger:
    def __init__(self):
        self.ages = {k: np.zeros((1000, 1000), dtype=np.uint64) for k in '123456'}
        self.completed = []

    def ingest(self, filename, data):
        ts, canvas, kind, _ = filename.split('-')
        ts = int(ts)
        if kind == 'f':
            self.ages[canvas].fill(ts)
        else:
            self.ages[canvas][nonzero_pixels(data)] = ts

def remove_pixelhistory(pa, debug=False):
    for line in open('pixelhistory'):
        parts = line.split(' ')
        if len(parts) == 6:
            # old style
            c, x, y, ts = parts[:4]
            x, y, ts = int(x), int(y), int(ts)
            a = int(pa.ages[c][x,y])
            if a != 0 and abs(a-ts)<10000:
                if debug:
                    print(a-ts, a, ts, a < ts)
        elif len(parts) == 7:
            c, x, y, ts, _ = parts[:5]
            x, y, ts = int(x), int(y), int(ts)
            a = int(pa.ages[c][x,y])
            if a != 0 and ts > a + 5000:
                if debug:
                    print(a-ts, a, ts, a < ts)
                pa.ages[c][x,y] = 0

def load_pa():
    try:
        pa = pickle.loads(zlib.decompress(open('pixelages.pickle.gz', 'rb').read()))
    except IOError:
        pa = PixelAger()
    return pa


def main():
    pa = load_pa()

    compstart = list(pa.completed)
    for zf in sorted(glob.glob('framedata_*.zip')):
        if zf in pa.completed:
            continue
        try:
            with zipfile.ZipFile(zf) as z:
                for f in z.namelist():
                    if '-f-' in f:
                        continue
                    data = z.read(f)
                    try:
                        pa.ingest(f, data)
                    except PIL.UnidentifiedImageError:
                        pass
            pa.completed.append(zf)
        except zipfile.BadZipFile:
            continue

    if pa.completed != compstart:
        print(pa.ages)
        with open('pixelages.pickle.gz', 'wb') as f:
            f.write(zlib.compress(pickle.dumps(pa)))

    remove_pixelhistory(pa)

    print(np.where(pa.ages['4']!=0)[0].shape[0] + np.where(pa.ages['1']!=0)[0].shape[0])


if __name__ == '__main__':
    main()
