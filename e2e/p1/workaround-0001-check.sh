#!/usr/bin/env bash
# e2e/p1/workaround-0001-check.sh — **로컬 테스트 DB 전용 우회** (레포 무수정, Lead 승인 2026-09-06).
# 결함: server/migrations/0001_init.sql:250 의 CHECK 가 lane.runtime_session_ref 에 키 'kind' 를 요구하지만
# contracts/harness.md §6 · contracts/protocol.go RuntimeSessionRef 는 'runtime_kind' 를 쓴다 → 데몬 finish 가 500.
# S 스트림의 0004 마이그레이션이 머지되면 이 스크립트는 필요 없다(적용 여부는 idempotent).
source "$(dirname "$0")/lib.sh"
psqlq "ALTER TABLE lane DROP CONSTRAINT IF EXISTS lane_runtime_session_ref_check;
ALTER TABLE lane ADD CONSTRAINT lane_runtime_session_ref_check CHECK (runtime_session_ref IS NULL OR ((runtime_session_ref ? 'kind' OR runtime_session_ref ? 'runtime_kind') AND runtime_session_ref ? 'session_id'));"
psqlq "select pg_get_constraintdef(oid) from pg_constraint where conname='lane_runtime_session_ref_check'"
ok "lane_runtime_session_ref_check: runtime_kind 허용 (로컬 DB 우회)"
