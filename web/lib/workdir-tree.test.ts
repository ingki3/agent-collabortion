/**
 * 경로 슬러그 파리티 — 서버 `workdirs.PathSlug` 와 웹 `pathSlug` 가 **같은 표**
 * (`server/internal/workdirs/testdata/path_slug_parity.json`)를 읽는다. 같은 규칙(daemon-protocol §6.1)을 두 번 구현하는
 * 자리라, 한쪽이 바뀌면(예: 40룬 → 40바이트) 그쪽 테스트가 빨개진다. 틀리면 S13 이 이름을 한 번도 바꾸지 않은 한글
 * 에이전트에 「만들 때의 이름」 배지를 잘못 붙인다(PR #345 리뷰).
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { pathSlug, renamedSince } from "./workdir-tree";
import type { Workdir } from "@/lib/api/types";

const TABLE = join(__dirname, "..", "..", "server", "internal", "workdirs", "testdata", "path_slug_parity.json");
const cases: { in: string; want: string }[] = JSON.parse(readFileSync(TABLE, "utf8")).cases;

describe("pathSlug ↔ 서버 PathSlug 파리티", () => {
  it("표가 비어 있지 않다", () => {
    expect(cases.length).toBeGreaterThanOrEqual(3);
  });
  it.each(cases)("$in → $want", (c) => {
    expect(pathSlug(c.in)).toBe(c.want);
  });
  it("이름을 바꾸지 않은 한글 에이전트에는 「만들 때의 이름」 배지가 없다", () => {
    for (const c of cases.slice(0, 3)) {
      const w = { path_or_ref: `/root/rooms/r-3f2a91c0/m-8b11de02/${c.want}-0c7e5d19` } as Workdir;
      expect(renamedSince(w, c.in)).toBe(false);
    }
  });
});
