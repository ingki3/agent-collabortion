/**
 * Media Preview · Lightbox(COMPONENTS §9.12 · SCREEN v0.19.12 §4.6 · PRD FR-4.3.1).
 *
 * 회귀 주입(끄면 그 괄호가 FAIL):
 *   mediaKind 가 content_type 대신 이름(확장자)을 보게 → (server-judged)
 *   mediaKind 의 IMAGE 표에 text/html 을 넣음 → (html-file)
 *   이미지를 <img> 대신 <object>/<iframe> 로 → (svg-img)
 *   inlineUrl 에서 ?inline=true 를 뺌 → (inline)
 *   video 의 preload/controls/autoplay 규칙을 바꿈 → (video)
 *   onError 폴백을 지움 → (fallback)
 *   Lightbox 의 Esc·←→ 처리를 지움 → (lightbox-keys)
 *   바깥 누르면 닫힘을 지움 → (lightbox-outside)
 *   MessageCard 가 attachments 를 그리지 않음 → (message)
 */
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { Lightbox, MediaGroup, MediaPreview } from "./MediaPreview";
import { MessageCard } from "./MessageCard";
import { attachmentsOnlyContent, formatBytes, inlineUrl, mediaKind } from "@/lib/media";
import type { Message } from "@/lib/api/types";

afterEach(cleanup);

const img = (id: string, name = `${id}.png`) => ({ id, name, version: 1, content_type: "image/png", size_bytes: 2048 });

describe("mediaKind — 서버가 판정한 content_type 만 본다", () => {
  it("(server-judged) 이름이 .png 여도 content_type 이 text/html 이면 파일 카드", () => {
    expect(mediaKind("text/html; charset=utf-8")).toBe("file");
    expect(mediaKind("image/svg+xml")).toBe("image");
    expect(mediaKind("IMAGE/PNG")).toBe("image");
    expect(mediaKind("video/webm")).toBe("video");
    expect(mediaKind("audio/mpeg")).toBe("audio");
    expect(mediaKind("audio/mp4")).toBe("audio");
    expect(mediaKind("application/octet-stream")).toBe("file");
    expect(mediaKind(null)).toBe("file");
    expect(mediaKind("image/bmp")).toBe("file");
  });
  it("크기·첨부만 본문", () => {
    expect(formatBytes(3.1 * 1024 * 1024)).toBe("3.1MB");
    expect(formatBytes(820 * 1024)).toBe("820KB");
    expect(attachmentsOnlyContent(2)).toBe("(파일 2개)");
    expect(inlineUrl("a b")).toBe("/api/v1/artifacts/a%20b/content?inline=true");
  });
});

describe("MediaPreview — 종류마다 모양", () => {
  it("(svg-img)(inline) 이미지는 <img> 썸네일, alt=파일 이름, ?inline=true 주소, 머리줄에 v·크기·내려받기", () => {
    render(<MediaPreview item={{ id: "s1", name: "logo.svg", version: 2, content_type: "image/svg+xml", size_bytes: 900 }} />);
    const el = screen.getByTestId("media-image") as HTMLImageElement;
    expect(el.tagName).toBe("IMG");
    expect(el.alt).toBe("logo.svg");
    expect(el.getAttribute("src")).toBe("/api/v1/artifacts/s1/content?inline=true");
    expect(document.querySelector("object, iframe, embed")).toBeNull();
    expect(screen.getByTestId("media-head").textContent).toContain("v2");
    expect(screen.getByTestId("media-open").textContent).toBe("내려받기");
    expect(screen.getByTestId("media-image-open").getAttribute("aria-label")).toBe("logo.svg 크게 보기");
  });

  it("(video) 영상은 controls · preload=metadata · 자동 재생 없음 · aria-label 「〈이름〉 재생」", () => {
    render(<MediaPreview item={{ id: "v1", name: "play.mp4", version: 1, content_type: "video/mp4", size_bytes: 1 }} />);
    const v = screen.getByTestId("media-video") as HTMLVideoElement;
    expect(v.hasAttribute("controls")).toBe(true);
    expect(v.getAttribute("preload")).toBe("metadata");
    expect(v.hasAttribute("autoplay")).toBe(false);
    expect(v.getAttribute("aria-label")).toBe("play.mp4 재생");
  });

  it("소리는 <audio controls> 한 줄 + 이름", () => {
    render(<MediaPreview item={{ id: "a1", name: "bgm.mp3", version: 1, content_type: "audio/mpeg", size_bytes: 3_250_585 }} />);
    const a = screen.getByTestId("media-audio") as HTMLAudioElement;
    expect(a.tagName).toBe("AUDIO");
    expect(a.hasAttribute("controls")).toBe(true);
    expect(screen.getByTestId("media-audio-meta").textContent).toContain("bgm.mp3");
    expect(screen.getByTestId("media-head").textContent).toContain("3.1MB");
  });

  it("(html-file) HTML·그 밖은 재생기 없이 파일 카드 + 「열기」", () => {
    render(<MediaPreview item={{ id: "h1", name: "evil.png", version: 1, content_type: "text/html; charset=utf-8", size_bytes: 10 }} />);
    expect(screen.getByTestId("media-preview").dataset.kind).toBe("file");
    expect(document.querySelector("img, video, audio")).toBeNull();
    expect(screen.getByTestId("media-open").textContent).toBe("열기");
  });

  it("(fallback) 못 불러오면 한 줄 문장 + 파일 카드(머리줄 그대로)", () => {
    render(<MediaPreview item={img("x")} />);
    fireEvent.error(screen.getByTestId("media-image"));
    expect(screen.getByTestId("media-failed").textContent).toBe("미리보기를 불러올 수 없습니다 — 내려받아 여세요");
    expect(screen.getByTestId("media-preview").dataset.kind).toBe("failed");
    expect(screen.getByTestId("media-open")).toBeTruthy();
  });
});

describe("라이트박스", () => {
  it("(lightbox-keys) 썸네일을 누르면 열리고 ✕ 에 초점, → ← 로 같은 묶음 이미지를 돌고 Esc 로 닫히며 초점이 썸네일로 돌아간다", async () => {
    render(<MediaGroup items={[img("a"), { id: "m", name: "bgm.mp3", version: 1, content_type: "audio/mpeg", size_bytes: 1 }, img("b"), img("c")]} />);
    const opens = screen.getAllByTestId("media-image-open");
    opens[0].focus();
    fireEvent.click(opens[0]);
    const box = screen.getByTestId("lightbox");
    expect(box.getAttribute("role")).toBe("dialog");
    expect(box.getAttribute("aria-modal")).toBe("true");
    expect(document.activeElement).toBe(screen.getByTestId("lightbox-close"));
    expect(screen.getByTestId("lightbox-close").getAttribute("aria-label")).toBe("닫기");
    const alt = () => (screen.getByTestId("lightbox-image") as HTMLImageElement).alt;
    expect(alt()).toBe("a.png");
    fireEvent.keyDown(window, { key: "ArrowRight" });
    expect(alt()).toBe("b.png"); // 소리는 건너뛴다 — 이미지끼리만
    fireEvent.keyDown(window, { key: "ArrowRight" });
    fireEvent.keyDown(window, { key: "ArrowRight" });
    expect(alt()).toBe("a.png"); // 끝에서 처음으로
    fireEvent.keyDown(window, { key: "ArrowLeft" });
    expect(alt()).toBe("c.png");
    expect(within(screen.getByTestId("lightbox-foot")).getByText("3/3", { exact: false })).toBeTruthy();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByTestId("lightbox")).toBeNull();
    await new Promise((r) => setTimeout(r, 5));
    expect(document.activeElement).toBe(opens[0]);
  });

  it("(lightbox-outside) 바깥(덮개)을 누르면 닫히고 이미지를 누르면 안 닫힌다 · 한 장이면 ←→ 버튼 없음", () => {
    let closed = 0;
    render(<Lightbox items={[img("one")]} index={0} onIndex={() => {}} onClose={() => closed++} />);
    expect(screen.queryByTestId("lightbox-next")).toBeNull();
    fireEvent.click(screen.getByTestId("lightbox-image"));
    expect(closed).toBe(0);
    fireEvent.click(screen.getByTestId("lightbox"));
    expect(closed).toBe(1);
    expect(screen.getByTestId("lightbox-download").getAttribute("href")).toBe("/api/v1/artifacts/one/content");
  });
});

describe("(message) 메시지 첨부 카드 — Message.attachments(게시 때 버전 그대로)", () => {
  it("사람 말풍선 아래에 첨부 카드, 버전은 AttachmentRef 그대로", () => {
    const m = {
      id: "m1", session_id: "r", author_type: "user", author_id: "u", author: { name: "Simplist" }, parent_id: null,
      content: "(파일 2개)", mentions: [], source_task_id: null, lane_id: null, kind: "text", state: "posted", created_at: new Date().toISOString(),
      attachments: [
        { artifact_id: "i1", name: "ae86.jpg", version: 3, type: "attachment", content_type: "image/jpeg", size_bytes: 1000 },
        { artifact_id: "s1", name: "bgm-ref.mp3", version: 1, type: "attachment", content_type: "audio/mpeg", size_bytes: 3_250_585 },
      ],
    } as unknown as Message;
    render(<MessageCard message={m} />);
    const g = screen.getByTestId("message-attachments");
    expect(g.getAttribute("aria-label")).toBe("붙인 파일");
    expect(within(g).getAllByTestId("media-preview").map((e) => e.dataset.kind)).toEqual(["image", "audio"]);
    expect(within(g).getAllByTestId("media-head")[0].textContent).toContain("v3");
  });
  it("첨부가 없으면 카드 없음", () => {
    const m = { id: "m2", session_id: "r", author_type: "user", author_id: "u", parent_id: null, content: "hi", mentions: [], source_task_id: null, lane_id: null, kind: "text", state: "posted", created_at: new Date().toISOString(), attachments: [] } as unknown as Message;
    render(<MessageCard message={m} />);
    expect(screen.queryByTestId("message-attachments")).toBeNull();
  });
});
