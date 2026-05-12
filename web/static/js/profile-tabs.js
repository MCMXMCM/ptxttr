export function bindProfileStatLinks(root = document) {
  const links = root.querySelectorAll("[data-profile-tab]");
  links.forEach((link) => {
    if (link.dataset.bound === "1") return;
    link.dataset.bound = "1";
    link.addEventListener("click", (event) => {
      const tabID = link.getAttribute("data-profile-tab") || "";
      const input = tabID ? root.querySelector(`#${tabID}`) : null;
      if (!(input instanceof HTMLInputElement)) return;
      event.preventDefault();
      input.checked = true;
      input.dispatchEvent(new Event("change", { bubbles: true }));
      const panelID = tabID.replace("tab", "panel");
      const panel = root.querySelector(`#${panelID}`);
      if (panel) panel.scrollIntoView({ block: "start", behavior: "smooth" });
    });
  });
}
