import { App } from "./app";
import { SettingsPage } from "./settings/page";
import "./style.css";

const root = document.getElementById("app")!;

if (location.pathname.replace(/\/+$/, "") === "/settings") {
  document.title = "Settings · Print Pipeline";
  const page = new SettingsPage();
  root.append(page.el);
  void page.load();
} else {
  const app = new App();
  root.append(app.el);
  void app.start();
}
