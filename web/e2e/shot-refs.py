#!/usr/bin/env python3
# 문서(*.md)가 가리키는 web/__screenshots__ 이미지가 실재하는지 — T-R2-M8 DoD. 저장소 루트에서: python3 web/e2e/shot-refs.py
# 이름(p1-·u1-·r2- 등 접두어)·범위(01~04, 01…06)·중괄호({light,dark})를 펼쳐 잰다. 범위는 번호에 빈칸이 있어 한 벌 중 하나라도 있으면 통과.
import re,glob,os,subprocess,sys
root='web/__screenshots__'
files=subprocess.run(['git','ls-files','*.md'],capture_output=True,text=True).stdout.split()
files+= [f for f in subprocess.run(['git','ls-files','--others','--exclude-standard','*.md'],capture_output=True,text=True).stdout.split()]
tok=re.compile(r'((?:web/)?(?:__screenshots__/)?(?:v018/)?(?:p[1-5]|u1|r15|r2|dev|m8)-[A-Za-z0-9_.~…*{},-]*\.png)')
bad=0;n=0
def expand(p):
    # {a,b}
    m=re.search(r'\{([^}]*)\}',p)
    if m:
        out=[]
        for alt in m.group(1).split(','): out+=expand(p[:m.start()]+alt+p[m.end():])
        return out
    m=re.search(r'(\d+)(?:~|…)(\d+)',p)
    if m:
        a,b=m.group(1),m.group(2); w=len(a)
        return sum([expand(p[:m.start()]+str(i).zfill(w)+p[m.end():]) for i in range(int(a),int(b)+1)],[])
    return [p]
for f in files:
    if f.startswith('e2e/p5/out'): continue
    for i,line in enumerate(open(f,encoding='utf-8',errors='ignore'),1):
        for t in tok.findall(line):
            rel=t.split('__screenshots__/')[-1]
            ex=expand(rel); n+=1
            def ok(p):
                q=os.path.join(root,p.replace('…','*'))
                return glob.glob(q) or glob.glob(q[:-4]+'-*.png')
            # 범위(01~15)는 한 벌 중 하나라도 있으면 통과(번호에 빈칸이 있다), 단일 이름·중괄호는 전부 있어야
            hits=[p for p in ex if ok(p)]
            rng=bool(re.search(r'\d(~|…)\d',rel))
            if (rng and not hits) or (not rng and len(hits)!=len(ex)):
                bad+=1; print(f'{f}:{i}: {t} MISSING {[p for p in ex if not ok(p)]}')
print(f"refs={n} missing={bad}"); sys.exit(1 if bad else 0)
