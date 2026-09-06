#!/usr/bin/env python3
"""기상 시스템 메시지가 질문 카드를 인용하는가(E3-05 (3)).
사용: quotes_card.py <body_file> <card_id> <question_note>  → yes|no
카드 id 를 담거나 질문 본문 앞부분을 담으면 인용으로 본다."""
import sys
body = open(sys.argv[1], encoding="utf-8").read()
card, note = sys.argv[2], (sys.argv[3] if len(sys.argv) > 3 else "").strip()
print("yes" if (card and card in body) or (note and note[:12] in body) else "no")
