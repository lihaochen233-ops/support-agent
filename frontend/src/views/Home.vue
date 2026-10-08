<script setup lang="ts">
import { onMounted, ref } from "vue";
import { refreshBootstrap, session } from "../api";
import Logo from "../components/Logo.vue";
import Icon from "../components/Icon.vue";
const serviceError = ref(false);
onMounted(() => refreshBootstrap().catch(() => (serviceError.value = true)));
</script>
<template>
  <div class="landing">
    <header class="landing-nav">
      <Logo />
      <nav>
        <a href="#how">如何运作</a
        ><RouterLink to="/login"
          >客服工作台<Icon name="arrow" :size="16"
        /></RouterLink>
      </nav>
    </header>
    <main>
      <section class="hero">
        <div class="hero-copy">
          <div class="eyebrow"><span class="status-dot" />有温度的智能服务</div>
          <h1>每一个问题，<br />都有<span>认真回应。</span></h1>
          <p class="hero-description">
            从第一句「你好」，到问题得到解决。<br />让 AI
            找到答案，让专业的人接住更复杂的需求。
          </p>
          <div class="hero-actions">
            <RouterLink to="/chat" class="btn btn-primary btn-large"
              >开始咨询<Icon name="arrow" :size="18" /></RouterLink
            ><a href="#how" class="text-link"
              >了解 Luma<Icon name="chevron" :size="16"
            /></a>
          </div>
          <div class="hero-assurances">
            <span><Icon name="check" :size="15" />无需注册</span
            ><span><Icon name="check" :size="15" />支持图片咨询</span
            ><span><Icon name="check" :size="15" />随时转接人工</span>
          </div>
        </div>
        <div class="hero-art" aria-label="AI 与人工协同服务示意">
          <div class="orbital orbital-one"></div>
          <div class="orbital orbital-two"></div>
          <div class="art-dot dot-one"></div>
          <div class="art-dot dot-two"></div>
          <div class="art-core">
            <span class="art-core-icon"><Icon name="spark" :size="49" /></span
            ><strong>Luma</strong><span>理解 · 检索 · 回应</span>
          </div>
          <div class="float-card knowledge-card">
            <span class="art-small-icon"><Icon name="book" :size="21" /></span>
            <div>
              <strong>回答有据可循</strong>
              <p>连接您的专属知识库</p>
            </div>
            <span class="tiny-check">✓</span>
          </div>
          <div class="float-card human-card">
            <div class="human-mini-avatar">
              <Icon name="headset" :size="25" /><i></i>
            </div>
            <div>
              <strong>服务无缝衔接</strong>
              <p>复杂问题，交给专业的人</p>
            </div>
          </div>
          <div class="art-label">
            <span class="status-dot" />AI + HUMAN, BETTER TOGETHER
          </div>
        </div>
      </section>
      <section id="how" class="how-section">
        <div class="section-intro">
          <span class="eyebrow">简单开始，妥善解决</span>
          <h2>让咨询回归轻松</h2>
          <p>一段连续的对话，一次完整的服务。</p>
        </div>
        <div class="feature-grid">
          <article>
            <span class="feature-number">01</span>
            <div class="feature-icon"><Icon name="chat" :size="25" /></div>
            <h3>像聊天一样提问</h3>
            <p>
              用文字描述，或直接发送截图。无需填写繁琐表单，也无需注册账号。
            </p>
          </article>
          <article>
            <span class="feature-number">02</span>
            <div class="feature-icon"><Icon name="book" :size="25" /></div>
            <h3>让答案有出处</h3>
            <p>智能助手检索知识库后回答，相关资料随附在消息中，方便您核实。</p>
          </article>
          <article>
            <span class="feature-number">03</span>
            <div class="feature-icon"><Icon name="headset" :size="25" /></div>
            <h3>需要时，人工接力</h3>
            <p>客服会接收完整的聊天记录与问题摘要，让您不用从头再说一遍。</p>
          </article>
        </div>
      </section>
      <div class="landing-service">
        <span class="status-dot" :class="{ offline: serviceError }" /><span>{{
          serviceError
            ? "服务暂时无法连接，请稍后再试"
            : session.bootstrap?.ai_enabled
              ? "智能助手已就绪"
              : "人工服务与访客留言已开放"
        }}</span
        ><span class="separator">/</span><span>认真对待每一次连接</span
        ><RouterLink to="/chat"
          >发起咨询<Icon name="arrow" :size="16"
        /></RouterLink>
      </div>
    </main>
    <footer class="landing-footer">
      <Logo /><span>让技术有用，让服务有温度。</span
      ><small>© {{ new Date().getFullYear() }} Luma</small>
    </footer>
  </div>
</template>
