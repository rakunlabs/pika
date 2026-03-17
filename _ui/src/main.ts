import "@/style/global.css";
import App from "@/App.svelte";
import { mount } from "svelte";
import axios from "axios";
import { basePath } from "@/lib/basepath";

// Configure axios to prepend the base path to all relative API calls.
axios.defaults.baseURL = basePath;

const app = mount(App, {
  target: document.body,
});

export default app;
