import "./globals.css";

export const metadata = {
  title: "Dev Guild",
  description: "Text the guild. Every message is written to the signed log.",
  applicationName: "Dev Guild",
  appleWebApp: {
    capable: true,
    title: "Dev Guild",
    statusBarStyle: "black-translucent",
  },
  icons: { icon: "/avatars/claude.png", apple: "/avatars/claude.png" },
};

export const viewport = {
  width: "device-width",
  initialScale: 1,
  maximumScale: 1,
  viewportFit: "cover",
  themeColor: "#000000",
};

export default function RootLayout({ children }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
