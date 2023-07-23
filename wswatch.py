#!/usr/bin/env python

import os
import json
import requests
import time
import zipfile
import redis

import msgpack
import websockets.exceptions
from websockets.sync.client import connect

import authparams



CONF_QUERY = '''subscription configuration($input: SubscribeInput!) {
  subscribe(input: $input) {
    id
    ... on BasicMessage {
      data {
        __typename
        ... on ConfigurationMessageData {
          colorPalette {
            colors {
              hex
              index
              __typename
            }
            __typename
          }
          canvasConfigurations {
            index
            dx
            dy
            __typename
          }
          activeZone {
            topLeft {
              x
              y
              __typename
            }
            bottomRight {
              x
              y
              __typename
            }
            __typename
          }
          canvasWidth
          canvasHeight
          adminConfiguration {
            maxAllowedCircles
            maxUsersPerAdminBan
            __typename
          }
          __typename
        }
      }
      __typename
    }
    __typename
  }
}'''

SUB_QUERY = '''subscription replace($input: SubscribeInput!) {
  subscribe(input: $input) {
    id
    ... on BasicMessage {
      data {
        __typename
        ... on FullFrameMessageData {
          __typename
          name
          timestamp
        }
        ... on DiffFrameMessageData {
          __typename
          name
          currentTimestamp
          previousTimestamp
        }
      }
      __typename
    }
    __typename
  }
}'''



def main():
    r = redis.Redis()

    sess = requests.Session()

    def s(d):
        w.send(json.dumps(d))

    ids = 0
    def nextid():
        nonlocal ids
        ids += 1
        return str(ids)

    ts = time.strftime('%y%m%d_%H%M%S', time.gmtime())

    f = open(f"data/wslog_{ts}.txt", "a")
    z = zipfile.ZipFile(f"data/framedata_{ts}.zip", 'w')

    have = set()

    with connect("wss://gql-realtime-2.reddit.com/query",
            additional_headers={
                "User-Agent": 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36',
                "Origin": "https://hot-potato.reddit.com"
            }) as w:
        print("hum")
        s({"type":"connection_init","payload":{"Authorization":"Bearer " + authparams.auth()}})
        s({"id":nextid(),"type":"start","payload":{"variables":{"input":{"channel":{"teamOwner":"GARLICBREAD","category":"CONFIG"}}},"extensions":{},"operationName":"configuration","query": CONF_QUERY}})
        while 1:
            m = json.loads(w.recv())

            try:
                m = m['payload']['data']['subscribe']['data']
                if m['name'] in have:
                    continue
                have.add(m['name'])
                dat = sess.get(m['name'])

                z.writestr(os.path.basename(m['name']), dat.content, compresslevel=0)
                m2 = dict(m)
                m2['data'] = dat.content
                m2d = msgpack.dumps(m2)
                r.publish('rplace_tiles', m2d)
                # r.xadd('rplace_tilestream', m2d)
            except KeyError:
                print(f"? {m}")
                md = json.dumps(m, separators=(',', ':'))
                f.write(md + '\n')

                if m.get('__typename') == 'ConfigurationMessageData':
                    for conf in m['canvasConfigurations']:
                        s({"id":nextid(),"type":"start","payload":{"variables":{"input":{"channel":{"teamOwner":"GARLICBREAD","category":"CANVAS","tag":str(conf['index'])}}},"extensions":{},"operationName":"replace","query": SUB_QUERY}})

                continue
            mt = m.get('__typename')

            for k in ('currentTimestamp', 'previousTimestamp'):
                if k in m:
                    m[k] = int(m[k])
            md = json.dumps(m, separators=(',', ':'))
            f.write(md + '\n')
            print(f"Received: {m}", len(dat.content))

if __name__ == '__main__':
    while True:
        try:
            main()
        except websockets.exceptions.WebSocketException:
            continue
        except KeyboardInterrupt:
            break
        break


