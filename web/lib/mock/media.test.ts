/**
 * 목이 서버와 같은 규칙을 흉내 내는지(PRD FR-4.3.1 · FR-3.7 · openapi v0.3.7) — 화면 e2e·스크린샷이 이 목 위에서 도므로
 * 목이 느슨하면 화면은 서버에서 안 되는 것을 그린다.
 *
 * 회귀 주입: judgeContentType 이 이름을 먼저 보게 하면 (spoof) FAIL; resolveAttachments 의 방 검사를 지우면 (other-room) FAIL;
 * inline 목록 검사를 지우면 (html-inline) FAIL; Range 분기를 지우면 (range) FAIL.
 */
import { describe, expect, it } from "vitest";
import { judgeContentType, resolveAttachments } from "./media";
import { installFetchBridge } from "./fetch-bridge";
import { dispatch, Problem, type Req } from "./handlers";
import { store } from "./store";

const b64 = (bytes: number[]) => Buffer.from(Uint8Array.from(bytes)).toString("base64");
const PNG = b64([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, ...Array(40).fill(0)]);
const HTML = Buffer.from("<!DOCTYPE html><html><script>alert(1)</script></html>").toString("base64");
const MP3 = b64([0x49, 0x44, 0x33, 3, 0, 0, 0, 0, 0, 0, ...Array(200).fill(0xab)]);

describe("judgeContentType — 서버 DetectContentType 과 같은 답", () => {
  it("(spoof) 서명이 이름보다 먼저 — .png 인 HTML 은 text/html", () => {
    const bytes = (s: string) => Uint8Array.from(Buffer.from(s, "base64"));
    expect(judgeContentType("evil.png", bytes(HTML))).toBe("text/html; charset=utf-8");
    expect(judgeContentType("shot.png", bytes(PNG))).toBe("image/png");
    expect(judgeContentType("bgm.mp3", bytes(MP3))).toBe("audio/mpeg");
    expect(judgeContentType("logo.svg", new TextEncoder().encode('<svg xmlns="http://www.w3.org/2000/svg"></svg>'))).toBe("image/svg+xml");
    expect(judgeContentType("notes.mp3", new TextEncoder().encode("plain text"))).toBe("text/plain; charset=utf-8");
  });
});

describe("목 — 올리기 · 미리보기 · 첨부", () => {
  async function room() {
    const bridge = await installFetchBridge();
    const me = await bridge.call<{ workspaces: { id: string }[] }>("GET", "/me");
    const r = await bridge.call<{ id: string }>("POST", `/workspaces/${me.body.workspaces[0].id}/rooms`, { name: "게임 제작" });
    return { bridge, roomId: r.body.id };
  }
  /**
   * 목을 직접 부른다 — 브리지의 `fetch` 는 jsdom 을 지나므로 `Range`·`Idempotency-Key` 같은 헤더를 실을 수 없다
   * (jsdom `Headers` 가 Range 를 지운다: 실제 브라우저에서도 Range 는 fetch 로 못 보내고 `<video>`·`<audio>` 가 스스로 보낸다).
   */
  const call = (method: string, path: string, body?: unknown, headers: Record<string, string> = {}) => {
    const u = new URL(path, "http://localhost");
    const req = {
      method, path: u.pathname, query: u.searchParams, body,
      headers: { get: (k: string) => headers[k.toLowerCase()] ?? null } as unknown as Headers,
      cookies: { colab_session: [...store().cookies.keys()][0] ?? "" },
    } satisfies Req;
    return dispatch(req);
  };
  /** multipart 는 route.ts 가 푸는 모양으로 직접 넣는다(브리지는 JSON 만 지난다). */
  async function upload(bridge: Awaited<ReturnType<typeof installFetchBridge>>, roomId: string, name: string, b64s: string, type = "attachment") {
    const res = await bridge.call<{ artifact: { id: string; content_type: string; version: number } }>("POST", `/rooms/${roomId}/artifacts`, {
      __multipart: true, fields: { name, type }, file: { name, type: "application/octet-stream", bytes: Uint8Array.from(Buffer.from(b64s, "base64")) },
    });
    return res;
  }

  it("올린 파일의 content_type 은 목이 판정한다 · 같은 이름은 다음 버전", async () => {
    const { bridge, roomId } = await room();
    const a = await upload(bridge, roomId, "shot.png", PNG);
    expect(a.status).toBe(201);
    expect(a.body.artifact.content_type).toBe("image/png");
    const b = await upload(bridge, roomId, "shot.png", PNG);
    expect(b.body.artifact.version).toBe(2);
    const spoof = await upload(bridge, roomId, "evil.png", HTML);
    expect(spoof.body.artifact.content_type).toBe("text/html; charset=utf-8");
    bridge.restore();
  });

  it("(other-room) attachment_ids — 같은 방만·중복 한 번·최대 10, 붙으면 Message.attachments", async () => {
    const { bridge, roomId } = await room();
    const img = (await upload(bridge, roomId, "ae86.png", PNG)).body.artifact.id;
    const other = await bridge.call<{ id: string }>("POST", `/workspaces/${(await bridge.call<{ workspaces: { id: string }[] }>("GET", "/me")).body.workspaces[0].id}/rooms`, { name: "다른 방" });
    const foreign = (await upload(bridge, other.body.id, "x.png", PNG)).body.artifact.id;

    const key = { "idempotency-key": "11111111-1111-4111-8111-111111111111" };
    const ok = await call("POST", `/rooms/${roomId}/messages`, { content: "이 사진처럼", attachment_ids: [img, img] }, key);
    expect(ok.status).toBe(201);
    const posted = (ok.body as { message: { attachments: { artifact_id: string }[] } }).message;
    expect(posted.attachments.map((a) => a.artifact_id)).toEqual([img]);

    const bad = await call("POST", `/rooms/${roomId}/messages`, { content: "x", attachment_ids: [foreign] }, { "idempotency-key": "22222222-2222-4222-8222-222222222222" });
    expect(bad.status).toBe(422);
    expect((bad.body as { errors: { code: string }[] }).errors[0].code).toBe("attachment_not_in_room");
    // 중복은 한 번으로 세므로 같은 id 11개는 한도에 걸리지 않는다 — 서로 다른 11개가 걸린다.
    const eleven = [] as string[];
    for (let i = 0; i < 11; i++) eleven.push((await upload(bridge, roomId, `f${i}.png`, PNG)).body.artifact.id);
    expect(resolveAttachments(store(), roomId, [img, img, ...eleven.slice(0, 9)], Problem as never)).toHaveLength(10);
    let threw = "";
    try {
      resolveAttachments(store(), roomId, eleven, Problem as never);
    } catch (e) {
      threw = (e as { extra?: { errors: { code: string }[] } }).extra?.errors[0].code ?? "";
    }
    expect(threw).toBe("too_many_attachments");
    bridge.restore();
  });

  it("(html-inline)(range) inline 은 미리보기 목록만 · nosniff·sandbox 늘 · 단일 Range 206 · 여러 범위 416", async () => {
    const { bridge, roomId } = await room();
    const png = (await upload(bridge, roomId, "shot.png", PNG)).body.artifact.id;
    const html = (await upload(bridge, roomId, "evil.png", HTML)).body.artifact.id;
    const mp3 = (await upload(bridge, roomId, "bgm.mp3", MP3)).body.artifact.id;
    const get = (id: string, q = "", range?: string) =>
      call("GET", `/artifacts/${id}/content${q}`, undefined, range ? { range } : {});
    const h = (r: Awaited<ReturnType<typeof call>>) => r.headers ?? {};

    let res = await get(png, "?inline=true");
    expect(h(res)["Content-Disposition"]).toMatch(/^inline/);
    expect(h(res)["Content-Type"]).toBe("image/png");
    expect(h(res)["X-Content-Type-Options"]).toBe("nosniff");
    expect(h(res)["Content-Security-Policy"]).toBe("sandbox");
    res = await get(html, "?inline=true");
    expect(h(res)["Content-Disposition"]).toMatch(/^attachment/);
    res = await get(png);
    expect(h(res)["Content-Disposition"]).toMatch(/^attachment/);

    res = await get(mp3, "", "bytes=10-19");
    expect(res.status).toBe(206);
    expect(h(res)["Content-Range"]).toMatch(/^bytes 10-19\/\d+$/);
    expect(h(res)["Content-Length"]).toBe("10");
    res = await get(mp3, "", "bytes=0-1,5-6");
    expect(res.status).toBe(416);
    expect(h(res)["Content-Range"]).toMatch(/^bytes \*\/\d+$/);
    bridge.restore();
  });
});
