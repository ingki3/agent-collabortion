#!/usr/bin/env python3
"""plan/spikes/spike04c/hermes_forget.py — Hermes 가 이 ACP 세션을 잊게 만든다 (E8-03 유도).

SPIKE_04a §(b) 와 **같은 방법**: `~/.hermes/state.db` 의 `sessions`(source='acp') 행과 그에 딸린
`messages` 행을 지운다. 지우는 것은 **이 스파이크가 방금 만든 세션 id 하나뿐**이다 — 사용자의
다른 세션은 건드리지 않는다(이 머신에서 다른 워커가 Hermes 를 쓰고 있다).

사용: hermes_forget.py <acp_session_id>
"""
import os, sqlite3, sys

sid = sys.argv[1]
if len(sid) != 36:
    sys.exit("refusing: not a session uuid: %r" % sid)
db = os.path.expanduser("~/.hermes/state.db")
con = sqlite3.connect(db)
cur = con.cursor()
row = cur.execute("select source, message_count from sessions where id = ?", (sid,)).fetchone()
if row is None:
    sys.exit("session %s not in %s" % (sid, db))
if row[0] != "acp":
    sys.exit("refusing: session %s is source=%s, not acp" % (sid, row[0]))
m = cur.execute("delete from messages where session_id = ?", (sid,)).rowcount
s = cur.execute("delete from sessions where id = ? and source = 'acp'", (sid,)).rowcount
con.commit()
con.close()
print("forgot %s: sessions=%d messages=%d (was message_count=%s)" % (sid, s, m, row[1]))
