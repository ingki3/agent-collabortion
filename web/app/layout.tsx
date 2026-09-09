import type { Metadata } from "next";
import "./tokens.css";
import "./app.css";
import { THEME_INIT } from "@/lib/theme";

export const metadata: Metadata = {
  title: "Colab",
  description: "Agent collaboration messaging",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="ko" suppressHydrationWarning>
      <head>
        {/* 저장된 테마를 첫 페인트 전에 <html> 에 심는다 — 밝은 화면이 한 프레임 번쩍이지 않게(§8.3). */}
        <script dangerouslySetInnerHTML={{ __html: THEME_INIT }} />
      </head>
      <body>{children}</body>
    </html>
  );
}
