/**
 * 화면 머리(COMPONENTS §8.5) — 제목 + **한 줄 설명** + 오른쪽 행동. 다섯 화면(세션·받은 요청·에이전트·
 * 연결된 컴퓨터·설정)이 전부 이것으로 시작한다. 설명은 "그 화면이 무엇을 하는 곳인지"를 §8.4 의 말(사용자의 말)로
 * 적는다 — 내부 용어(lane·task·HITL·런타임)는 문구 자물쇠(`lib/wording.test.ts`)가 막고, 이 표의 문구가 **실제로
 * 화면에 쓰이는지**도 같은 자물쇠가 잰다(PR #188 NN4 — "옛말 0건"만 재던 자물쇠에 "새말 존재"를 더했다).
 *
 * 비활성 버튼의 사유는 `DisabledHint` 로 버튼 바로 아래에 둔다(예전엔 빈 상태 카드에만 있어 눌러 보기 전엔
 * 몰랐다). 버튼은 같은 문구를 `title` 로, `aria-describedby` 로 그 힌트를 가리킨다.
 */
import "./page-head.css";

export type Screen = "sessions" | "inbox" | "agents" | "computers" | "settings";

/** 제목은 내비 라벨과 같은 말(AppNav.NAV_ITEMS). 설명은 한 줄 — 넘어가면 그 화면이 두 가지 일을 한다는 뜻이다. */
export const PAGE_COPY: Record<Screen, { title: string; desc: string }> = {
  sessions: { title: "세션", desc: "에이전트 팀에게 맡긴 일 하나가 세션입니다. 진행을 보고 새 일을 시작합니다." },
  inbox: { title: "받은 요청", desc: "에이전트가 사람의 답을 기다리는 요청입니다. 여기서 답하면 멈춘 일이 이어집니다." },
  agents: { title: "에이전트", desc: "함께 일할 에이전트를 만들고 역할과 지시를 정합니다." },
  computers: { title: "연결된 컴퓨터", desc: "에이전트가 실제로 실행되는 컴퓨터입니다. 연결 상태와 쓰는 중인 세션을 봅니다." },
  // 할 수 있는 일을 말한다(§8.5 · PR #191 NN2) — 지금 그 화면에 실제로 있는 것만: 테마 선택. 워크스페이스 설정은 아직 자리다.
  settings: { title: "설정", desc: "이 브라우저의 화면 테마를 고릅니다. 워크스페이스 설정은 준비 중입니다." },
};

export interface PageHeadProps {
  screen: Screen;
  /** 오른쪽 행동(버튼·카운트). 비활성 사유는 `DisabledHint` 로 같이 넘긴다. */
  children?: React.ReactNode;
}

export function PageHead({ screen, children }: PageHeadProps) {
  const copy = PAGE_COPY[screen];
  return (
    <div className="page-head" data-testid="page-head" data-screen={screen}>
      <div className="page-head__text">
        <h1>{copy.title}</h1>
        <p className="page-head__desc" data-testid="page-desc">{copy.desc}</p>
      </div>
      {children && <div className="page-head__actions">{children}</div>}
    </div>
  );
}

/**
 * 비활성 버튼의 사유 — 버튼 근처에서 말한다. `id` 를 버튼의 `aria-describedby` 에 넣는다.
 * 버튼이 활성이면 호출부가 아예 그리지 않는다(사유가 없는데 자리만 남기지 않는다).
 */
export function DisabledHint({ id, children }: { id: string; children: React.ReactNode }) {
  return (
    <p className="disabled-hint" id={id} data-testid={id}>
      {children}
    </p>
  );
}

export default PageHead;
