import { createApp } from 'vue'
import App from './App.vue'
import router from './router.js'
import { setUnauthorizedHandler } from './api.js'
import './style.css'

setUnauthorizedHandler(() => {
  if (router.currentRoute.value.name !== 'login') {
    router.push({ name: 'login' })
  }
})

createApp(App).use(router).mount('#app')
