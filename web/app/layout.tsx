import type { Metadata } from "next";
import "./tokens.css";
import "./app.css";

export const metadata: Metadata = {
  title: "Colab",
  description: "Agent collaboration messaging",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="ko">
      <body>{children}</body>
    </html>
  );
}
