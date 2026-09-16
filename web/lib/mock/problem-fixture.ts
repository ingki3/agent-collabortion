/**
 * 화면 테스트용 `ApiError` 픽스처(W-13, PR #212 리뷰 NN2).
 *
 * 화면 테스트가 서버 오류를 흉내 낼 때 `new ApiError({ type: "about:blank", title: "…", status, code, detail })` 를 손으로 적으면
 * (1) `type` 이 서버 모양(`https://colab.dev/problems/<code>`, problem.go)이 아니고 (2) `title` 을 상태와 다르게 적을 수 있다.
 * 여기서는 목·서버가 같이 쓰는 `TITLE` 표(apperr.go 의 titles 와 항목 단위로 같다 — server-wording (b))로 title 을 정하고 `type` 을 code 로 만든다.
 * 테스트가 정하는 것은 **code·status·detail·확장 칸**뿐이다.
 *
 * `lib/mock/` 에 두는 이유: 문구 자물쇠(`lib/wording.test.ts`)의 범위 밖(EXCLUDE)이라 픽스처의 문장이 화면 문구로 세지 않는다.
 */
import { ApiError } from "@/lib/api/client";
import type { Problem } from "@/lib/api/types";
import { titleOf } from "./wording";

export interface ProblemFixtureOptions {
  /** 서버 `Problem.detail` — 화면이 그대로 보이는 문장. */
  detail?: string;
  /** 422 의 `errors[]`. */
  errors?: Problem["errors"];
  /** `Problem` 확장 칸(`workdirs[]`·`sessions[]`·`can_respond_from` …) — additionalProperties: true. */
  extra?: Record<string, unknown>;
}

/** `problemFixture("session_active", 409, { detail: "진행 중인 세션은 먼저 종료하세요" })`. */
export function problemFixture(code: string, status: number, opts: ProblemFixtureOptions = {}): ApiError {
  return new ApiError({
    type: `https://colab.dev/problems/${code}`,
    title: titleOf(status),
    status,
    code,
    detail: opts.detail,
    errors: opts.errors,
    ...(opts.extra ?? {}),
  });
}
