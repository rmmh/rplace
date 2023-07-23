import asyncio
import argparse
import os
import sys
import time
import traceback

import numpy as np
import requests
import httpx

import authparams
import canvaswatcher

PixelAger = canvaswatcher.PixelAger  # pickle is dumb.


headers = {
    'authority': 'gql-realtime-2.reddit.com',
    'accept': '*/*',
    'accept-language': 'en-US,en;q=0.9',
    'apollographql-client-name': 'garlic-bread',
    'apollographql-client-version': '0.0.1',
    'content-type': 'application/json',
    'dnt': '1',
    'origin': 'https://garlic-bread.reddit.com',
    'referer': 'https://garlic-bread.reddit.com/',
    'sec-ch-ua': '"Not/A)Brand";v="99", "Google Chrome";v="115", "Chromium";v="115"',
    'sec-ch-ua-mobile': '?0',
    'sec-ch-ua-platform': '"Linux"',
    'sec-fetch-dest': 'empty',
    'sec-fetch-mode': 'cors',
    'sec-fetch-site': 'same-site',
    'user-agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36',
}

client = httpx.AsyncClient()

async def fetch_history(coords, output=True):
    vars = {}
    var_to_coord = {}

    args = []
    muts = []

    for c, x, y in coords:
            vn = f'c{c}_{x}_{y}'
            var_to_coord[vn] = (c, x, y)
            args.append(f'${vn}: ActInput!')
            vars[f'{vn}'] = {
                'actionName': 'r/replace:get_tile_history',
                'PixelMessageData': {
                    'coordinate': {
                        'x': x,
                        'y': y,
                    },
                    'colorIndex': 1,
                    'canvasIndex': c,
                },
            }
            muts.append(f'{vn}: act(input: ${vn})' + ' { data { ... on BasicMessage { data { ... on GetTileHistoryResponseMessageData { lastModifiedTimestamp userInfo { userID username } } } } } }')

    json_data = {
        'operationName': 'pixelHistory',
        'variables': vars,
        'query': '''mutation pixelHistory(%s) { %s }''' % (','.join(args), ' '.join(muts)),
    }

    now = int(time.time()*1000)

    response = await client.post('https://gql-realtime-2.reddit.com/query', headers=headers, json=json_data, timeout=60)
    ret = []
    j = response.json()
    if not j.get('data'):
        if j.get('error', {}).get('reason') == 'UNAUTHORIZED':
            headers['authorization'] = 'Bearer ' + authparams.auth()
            response = await client.post('https://gql-realtime-2.reddit.com/query', headers=headers, json=json_data, timeout=60)
            j = response.json()
    if not j.get('data'):
        raise ValueError(response.text)
    for k, v in j['data'].items():
        d = v['data'][0]['data']
        if not d['userInfo']:
            ret.append((*var_to_coord[k], now, 0, '', 'empty'))
        else:
            ret.append((*var_to_coord[k], now, int(d['lastModifiedTimestamp']), d['userInfo']['userID'][3:], d['userInfo']['username']))

    if output:
        for x in ret:
            print(*x)
    return ret

async def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--fresh', action='store_true')
    parser.add_argument('--redis', action='store_true')
    opts = parser.parse_args()

    coords = []

    if opts.fresh:
        pa = canvaswatcher.load_pa()
        canvaswatcher.remove_pixelhistory(pa)
        for c, a in pa.ages.items():
            c = int(c)
            for y, x in sorted(zip(*canvaswatcher.np.where(a != 0)), key=a.__getitem__):
                x = int(x)
                y = int(y)
                coords.append((c, x, y))
    elif opts.redis:
        import redis, msgpack
        r = redis.Redis()
        p = r.pubsub()
        p.subscribe('rplace_tiles')
        BATCH_SIZE = 1000
        coords = {}
        gts = {}
        while True:
            try:
                m = p.get_message(ignore_subscribe_messages=True, timeout=.1)
                if not m:
                    if coords:
                        fetchcoords = sorted(k for k,v in sorted(coords.items(), key=lambda x:x[1])[:BATCH_SIZE*4])
                        futs = []
                        for x in range(0, len(fetchcoords), BATCH_SIZE):
                            futs.append(fetch_history(fetchcoords[x:x+BATCH_SIZE], output=False))
                        for fut in futs:
                            for tup in await fut:
                                print(*tup)
                                coord = tuple(tup[:3])
                                gts[coord] = tup[4]
                                coords.pop(coord, None)
                    continue
                m = msgpack.loads(m['data'])
                if '-f-' in m['name']:
                    continue
                try:
                    nz = canvaswatcher.nonzero_pixels(m['data'])
                except Exception:
                    continue
                ts, c = os.path.basename(m['name']).split('-')[:2]
                ts = int(ts)
                c = int(c)
                for y, x in zip(*nz):
                    coord = (c, int(x), int(y))
                    if gts.get(coord, 0) + 1000 > ts:
                        continue
                    coords[coord] = ts
            except Exception:
                traceback.print_exc()
                continue
    else:
        canvas = 1
        for y in range(719, 1000):
            for x in range(1000):
                coords.append((canvas, x, y))
        canvas = 4
        for y in range(0, 500):
            for x in range(1000):
                coords.append((canvas, x, y))

    print("TODO:", len(coords), file=sys.stderr)

    BATCH_SIZE = 1000
    for x in range(0, len(coords), BATCH_SIZE):
        await fetch_history(coords[x:x+BATCH_SIZE])

if __name__ == '__main__':
    asyncio.run(main())
