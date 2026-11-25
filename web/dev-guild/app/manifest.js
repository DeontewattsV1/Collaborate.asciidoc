export default function manifest() {
  return {
    name: "Dev Guild",
    short_name: "Dev Guild",
    description: "iMessage with the guild",
    start_url: "/",
    display: "standalone",
    background_color: "#000000",
    theme_color: "#000000",
    icons: [
      { src: "/avatars/claude.png", sizes: "384x384", type: "image/png", purpose: "any" },
    ],
  };
}
