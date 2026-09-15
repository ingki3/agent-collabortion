import { redirect } from "next/navigation";

// 로그인 후 기본 화면은 S5 Sessions(SCREEN §2.1). 인증 판정은 앱 셸이 한다.
export default function Home() {
  redirect("/sessions");
}
