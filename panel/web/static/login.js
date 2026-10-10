"use strict";
VB.$("#login-form").addEventListener("submit", (e) => {
  // Show progress while the server checks the password (bcrypt takes a moment).
  setTimeout(() => VB.busy(e.target.querySelector("button[type=submit]"), true), 0);
});
