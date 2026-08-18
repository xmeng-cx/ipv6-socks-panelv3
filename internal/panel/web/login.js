const form = document.getElementById("loginForm");
const errorNode = document.getElementById("loginError");
const button = document.getElementById("loginButton");
form.addEventListener("submit", async (event) => {
  event.preventDefault(); button.disabled = true; errorNode.classList.add("hidden");
  try {
    const response = await fetch("/api/v1/auth/login", {method:"POST", headers:{"Content-Type":"application/json"}, body:JSON.stringify({username:document.getElementById("loginUsername").value.trim(),password:document.getElementById("loginPassword").value})});
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data?.error?.message || "登录失败");
    location.replace("/");
  } catch (error) { errorNode.textContent = error.message; errorNode.classList.remove("hidden"); button.disabled = false; }
});
