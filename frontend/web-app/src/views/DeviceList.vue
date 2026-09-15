<template>
  <div class="device-list-page">
    <!-- 移动端紧凑页头：两行高密度控件 (桌面端已合并至全局顶栏) -->
    <header v-if="isMobile" class="page-header mobile-header">
      <!-- 单行紧凑页头：批量入口（admin）/ 标签 / 排序 / 宫格 / 搜索 / 刷新 / 视图切换 -->
      <div class="mh-row">
        <!-- 批量操作弹层（仅 admin）：群控 + 预览开关 + 标签管理/全局设置 -->
        <div v-if="authStore.isAdmin" class="mh-dropdown">
          <button class="mh-filter-btn" @click.stop="toggleMobileMenu('batch')">☰ 批量 ▾</button>
          <div v-if="mobileOpenMenu === 'batch'" class="mh-panel" @click.stop>
            <button class="mh-panel-item" @click="toggleMobileGroupControl">
              {{ groupControlStore.isGroupControlActive ? '退出群控' : '进入群控' }}
            </button>
            <!-- 群控激活时的快捷操作（与桌面端群控工具栏等价） -->
            <template v-if="groupControlStore.isGroupControlActive">
              <button class="mh-panel-item" @click.stop="selectAllOnline">全选在线</button>
              <button class="mh-panel-item" @click.stop="clearSlaves">清空已选</button>
              <div class="tag-select-dropdown">
                <button class="mh-panel-item dropdown-trigger" @click.stop="showTagDropdown = !showTagDropdown">
                  按标签勾选 ▾
                </button>
                <div v-if="showTagDropdown" class="tag-dropdown-menu" @click.stop>
                  <div
                    v-for="tag in tagStore.tags"
                    :key="tag.id"
                    class="tag-dropdown-item"
                    @click="selectByTag(tag.id)"
                  >
                    <span class="tag-color-dot" :style="{ backgroundColor: tag.color }"></span>
                    <span class="tag-name-text">{{ tag.name }}</span>
                  </div>
                  <div v-if="tagStore.tags.length === 0" class="tag-dropdown-empty">暂无标签</div>
                </div>
              </div>
              <div class="mh-panel-static">已选 {{ groupControlStore.selectedSlaveIds.length }} 台</div>
              <button
                v-if="groupControlStore.selectedSlaveIds.length > 0"
                class="mh-panel-item"
                @click="openTagManager('batch'); closeMobileMenus()"
              >批量打标签</button>
            </template>
            <div class="mh-panel-divider"></div>
            <!-- 高频预览 / 预览直控开关（v-model 绑定与桌面端一致） -->
            <label class="switch-label mh-switch" title="开启后，可视区域内的虚机将使用 WebCodecs 硬件加速播放 10fps 实时预览">
              <input
                type="checkbox"
                v-model="deviceStore.globalPreviewMode"
                class="switch-checkbox"
              >
              <span class="switch-text">高频预览</span>
            </label>
            <label
              class="switch-label mh-switch"
              :class="{ 'disabled': !deviceStore.globalPreviewMode }"
              title="开启后，可直接点击列表里的预览画面进行触控和按键控制，无需进入详情页 (需要先开启高频预览)"
            >
              <input
                type="checkbox"
                v-model="deviceStore.globalInteractiveMode"
                :disabled="!deviceStore.globalPreviewMode"
                class="switch-checkbox"
              >
              <span class="switch-text">预览直控</span>
            </label>
            <div class="mh-panel-divider"></div>
            <button class="mh-panel-item" @click="openTagManager('full'); closeMobileMenus()">标签管理</button>
            <button class="mh-panel-item" @click="openGlobalSettings(); closeMobileMenus()">全局设置</button>
          </div>
        </div>
        <!-- 标签筛选（全部 / 各标签 / 离线设备） -->
        <div class="mh-dropdown">
          <button
            class="mh-filter-btn"
            :class="{ active: tagStore.selectedTagIds.length > 0 || deviceStore.showOfflineOnly }"
            @click.stop="toggleMobileMenu('tag')"
          >标签 ▾</button>
          <div v-if="mobileOpenMenu === 'tag'" class="mh-panel" @click.stop>
            <button
              class="mh-panel-item"
              :class="{ active: tagStore.selectedTagIds.length === 0 && !deviceStore.showOfflineOnly }"
              @click="selectAllTags(); closeMobileMenus()"
            >
              <span class="tag-dot all"></span>
              <span class="mh-item-name">全部设备</span>
              <span class="mh-item-count">{{ deviceStore.devices.length }}</span>
            </button>
            <button
              v-for="tag in tagStore.tags"
              :key="tag.id"
              class="mh-panel-item"
              :class="{ active: tagStore.selectedTagIds.includes(tag.id) }"
              @click="toggleSelectedTag(tag.id); closeMobileMenus()"
            >
              <span class="tag-dot" :style="{ background: tag.color }"></span>
              <span class="mh-item-name">{{ tag.name }}</span>
              <span class="mh-item-count">{{ getTagDeviceCount(tag.id) }}</span>
            </button>
            <button
              class="mh-panel-item"
              :class="{ active: deviceStore.showOfflineOnly }"
              @click="toggleOfflineView(); closeMobileMenus()"
            >
              <span class="tag-dot offline"></span>
              <span class="mh-item-name">离线设备</span>
              <span class="mh-item-count">{{ deviceStore.offlineDevices.length }}</span>
            </button>
            <template v-if="authStore.isAdmin">
              <div class="mh-panel-divider"></div>
              <button class="mh-panel-item" @click="openTagManager('full'); closeMobileMenus()">标签管理</button>
            </template>
          </div>
        </div>
        <!-- 排序 -->
        <div class="mh-dropdown">
          <button class="mh-filter-btn" :class="{ active: sortBy !== 'default' }" @click.stop="toggleMobileMenu('sort')">排序 ▾</button>
          <div v-if="mobileOpenMenu === 'sort'" class="mh-panel" @click.stop>
            <button class="mh-panel-item" :class="{ active: sortBy === 'default' }" @click="setSortBy('default')">默认排序</button>
            <button class="mh-panel-item" :class="{ active: sortBy === 'recent' }" @click="setSortBy('recent')">最近活跃</button>
          </div>
        </div>
        <!-- 宫格列数（面板右对齐防溢出） -->
        <div class="mh-dropdown drop-right">
          <button class="mh-filter-btn" @click.stop="toggleMobileMenu('cols')">宫格 ▾</button>
          <div v-if="mobileOpenMenu === 'cols'" class="mh-panel" @click.stop>
            <button
              v-for="n in [2, 3, 4]"
              :key="n"
              class="mh-panel-item"
              :class="{ active: mobileCols === n }"
              @click="setMobileCols(n)"
            >{{ n }} 列</button>
          </div>
        </div>
        <!-- 账号剩余时间（仅账号设有有效期时显示） -->
        <span
          v-if="accountExpiryChip"
          class="mh-expiry-chip"
          :class="{ expired: accountExpired }"
          :title="accountExpiryTime ? '账号到期时间: ' + accountExpiryTime.toLocaleString('zh-CN', { hour12: false }) : ''"
        >⏳ {{ accountExpiryChip }}</span>
        <div class="mh-actions">
          <button class="mh-icon-btn" :class="{ active: showMobileSearch }" @mousedown.prevent @click.stop="toggleMobileSearch" title="搜索" aria-label="搜索">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
              <circle cx="11" cy="11" r="7"></circle>
              <path d="M20 20l-4-4"></path>
            </svg>
          </button>
          <button class="mh-icon-btn" @click="refreshDevices" title="刷新设备列表" aria-label="刷新">⟳</button>
          <button class="mh-icon-btn" @click="toggleViewMode" :title="viewMode === 'grid' ? '切换到列表视图' : '切换到卡片视图'" aria-label="切换视图">
            <svg v-if="viewMode === 'grid'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
              <line x1="8" y1="6" x2="21" y2="6"></line>
              <line x1="8" y1="12" x2="21" y2="12"></line>
              <line x1="8" y1="18" x2="21" y2="18"></line>
              <line x1="3" y1="6" x2="3.01" y2="6"></line>
              <line x1="3" y1="12" x2="3.01" y2="12"></line>
              <line x1="3" y1="18" x2="3.01" y2="18"></line>
            </svg>
            <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
              <rect x="3" y="3" width="7" height="7"></rect>
              <rect x="14" y="3" width="7" height="7"></rect>
              <rect x="14" y="14" width="7" height="7"></rect>
              <rect x="3" y="14" width="7" height="7"></rect>
            </svg>
          </button>
        </div>
      </div>

      <!-- 搜索展开态：整行搜索输入框 -->
      <div v-if="showMobileSearch" class="mh-search-row">
        <div class="search-box">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
            <circle cx="11" cy="11" r="7"></circle>
            <path d="M20 20l-4-4"></path>
          </svg>
          <input
            ref="mobileSearchInput"
            v-model="searchQuery"
            type="search"
            placeholder="搜索设备或标签"
            @blur="onMobileSearchBlur"
          >
        </div>
      </div>
    </header>

    <div class="content-layout">
      <section class="mobile-tag-bar">
        <button
          class="tag-filter"
          :class="{ active: tagStore.selectedTagIds.length === 0 && !deviceStore.showOfflineOnly }"
          @click="selectAllTags"
        >
          <span class="tag-dot all"></span>
          <span class="tag-name">全部设备</span>
          <span class="tag-count">{{ deviceStore.devices.length }}</span>
        </button>
        <button
          v-for="tag in tagStore.tags"
          :key="tag.id"
          class="tag-filter"
          :class="{ active: tagStore.selectedTagIds.includes(tag.id) }"
          :style="tagFilterStyle(tag)"
          @click="toggleSelectedTag(tag.id)"
        >
          <span class="tag-dot" :style="{ background: tag.color }"></span>
          <span class="tag-name">{{ tag.name }}</span>
          <span class="tag-count">{{ getTagDeviceCount(tag.id) }}</span>
        </button>
        <button
          class="tag-filter"
          :class="{ active: deviceStore.showOfflineOnly }"
          @click="toggleOfflineView"
        >
          <span class="tag-dot offline"></span>
          <span class="tag-name">离线设备</span>
          <span class="tag-count">{{ deviceStore.offlineDevices.length }}</span>
        </button>
      </section>

      <!-- 群控快捷操作工具条 (Group Control Toolbar) -->
      <section v-if="groupControlStore.isGroupControlActive" class="group-control-bar animate-fade-in" @click.stop>
        <div class="gc-bar-left">
          <span class="gc-brand-badge">⚡ 群控模式</span>
          <button class="gc-btn" @click.stop="selectAllOnline" title="全选所有在线虚机">
            全选在线
          </button>
          <button class="gc-btn" @click.stop="clearSlaves" title="清空所有已选从机">
            清空已选
          </button>

          <!-- 按标签勾选下拉菜单 -->
          <div class="gc-tag-dropdown-wrap" @click.stop>
            <button class="gc-btn gc-dropdown-btn" @click.stop="showTagDropdown = !showTagDropdown">
              <span>按标签勾选 ▾</span>
            </button>
            <div v-if="showTagDropdown" class="gc-tag-dropdown-menu" @click.stop>
              <div 
                v-for="tag in tagStore.tags" 
                :key="tag.id" 
                class="gc-tag-item"
                @click="selectByTag(tag.id)"
              >
                <span class="gc-tag-dot" :style="{ backgroundColor: tag.color }"></span>
                <span class="gc-tag-name">{{ tag.name }}</span>
              </div>
              <div v-if="tagStore.tags.length === 0" class="gc-tag-empty">暂无可用标签</div>
            </div>
          </div>

          <span class="gc-count-badge">已勾选 {{ groupControlStore.selectedSlaveIds.length }} 台从机</span>

          <!-- 预览直控按键 -->
          <button 
            class="gc-btn gc-interactive-btn"
            :class="{ active: deviceStore.globalInteractiveMode }"
            @click.stop="toggleGlobalInteractive"
            title="开启后可直接在大盘预览画面上触控操作，无需进入详情页"
          >
            <span class="gc-btn-icon">🎮</span>
            <span>预览直控 {{ deviceStore.globalInteractiveMode ? '已开启' : '已关闭' }}</span>
          </button>

          <button 
            v-if="groupControlStore.selectedSlaveIds.length > 0"
            class="gc-btn gc-tag-action-btn"
            @click="openTagManager('batch')"
            title="对已勾选设备批量打标签"
          >
            🏷 批量打标签
          </button>
        </div>

        <div class="gc-bar-right">
          <button class="gc-exit-btn" @click="groupControlStore.toggleGroupControl(false)" title="退出群控模式">
            退出群控 ✕
          </button>
        </div>
      </section>

      <main class="grid-container">
        <div v-if="deviceStore.loading && deviceStore.devices.length === 0" class="state-view">
          <div class="spinner"></div>
          <p>正在获取虚机列表...</p>
        </div>

        <div v-else-if="deviceStore.devices.length === 0 && deviceStore.offlineDevices.length === 0" class="state-view">
          <div class="empty-icon">🔌</div>
          <h3>{{ authStore.isAdmin ? '接入第一台 Android 设备' : '暂无已分配的设备' }}</h3>
          <p>{{ authStore.isAdmin ? '在部署中心生成 ADB 部署包或 Magisk/KSU 模块，也可使用 WebUSB 连接设备。' : '请联系管理员分配设备访问权限。' }}</p>
          <button v-if="authStore.isAdmin" class="deploy-start-btn" @click="goToDeploy">打开部署中心</button>
        </div>

        <div v-else-if="noVisibleDevices" class="state-view">
          <div class="empty-icon">🔎</div>
          <h3>没有匹配结果</h3>
          <p>调整搜索关键字或标签筛选</p>
        </div>

        <div v-else>
          <!-- 高密运维数据表格视图 -->
          <template v-if="isTableView">
            <div class="device-table-container">
              <div class="device-table-header">
                <div class="th col-select">
                  <span v-if="groupControlStore.isGroupControlActive">
                    <input type="checkbox" :checked="isAllSelected" @change="toggleSelectAll" title="全选/取消全选" class="header-checkbox" />
                  </span>
                </div>
                <div class="th col-thumb">预览</div>
                <div class="th col-device sortable" @click="handleTableSort('id')" title="点击排序">
                  设备标识 <span class="sort-icon">{{ getSortIcon('id') }}</span>
                </div>
                <div class="th col-status sortable" @click="handleTableSort('status')" title="点击排序">
                  运行状态 <span class="sort-icon">{{ getSortIcon('status') }}</span>
                </div>
                <div class="th col-clients">接入情况</div>
                <div class="th col-metrics sortable" @click="handleTableSort('cpu')" title="点击按 CPU 占用排序">
                  系统负载 <span class="sort-icon">{{ getSortIcon('cpu') }}</span>
                </div>
                <div class="th col-tags">标签</div>
                <div class="th col-actions">快捷操作</div>
              </div>

              <div class="device-table-body">
                <template v-if="deviceStore.showOfflineOnly">
                  <DeviceListItem
                    v-for="device in sortedOfflineDevices"
                    :key="device.id"
                    :device="device"
                    :tags="tagStore.getTagsForDevice(device.id)"
                    @connect="connectDevice"
                    @settings="openSettings"
                    @edit-tags="id => openTagManager('single', id)"
                    @share="openShareModal"
                  />
                </template>
                <template v-else>
                  <DeviceListItem
                    v-for="device in sortedDevices"
                    :key="device.id"
                    :device="device"
                    :tags="tagStore.getTagsForDevice(device.id)"
                    @connect="connectDevice"
                    @settings="openSettings"
                    @edit-tags="id => openTagManager('single', id)"
                    @share="openShareModal"
                  />

                  <!-- 离线设备分区（在表格中直接无缝衔接） -->
                  <template v-if="sortedOfflineDevices.length > 0">
                    <div class="table-offline-divider">
                      <span>离线设备 ({{ sortedOfflineDevices.length }})</span>
                    </div>
                    <DeviceListItem
                      v-for="device in sortedOfflineDevices"
                      :key="device.id"
                      :device="device"
                      :tags="tagStore.getTagsForDevice(device.id)"
                      @connect="connectDevice"
                      @settings="openSettings"
                      @edit-tags="id => openTagManager('single', id)"
                      @share="openShareModal"
                    />
                  </template>
                </template>
              </div>
            </div>
          </template>

          <!-- 卡片网格视图 -->
          <template v-else>
            <!-- 离线筛选视图：只显示离线设备 -->
            <div
              v-if="deviceStore.showOfflineOnly"
              class="device-grid offline-grid"
              :style="{ gridTemplateColumns: gridColumnsStyle }"
            >
              <DeviceCard
                v-for="device in filteredOfflineDevices"
                :key="device.id"
                :device="device"
                :tags="tagStore.getTagsForDevice(device.id)"
                @connect="connectDevice"
                @settings="openSettings"
                @edit-tags="id => openTagManager('single', id)"
              />
            </div>
            <template v-else>
              <div 
                v-if="filteredDevices.length > 0"
                class="device-grid" 
                :style="{ gridTemplateColumns: gridColumnsStyle }"
              >
                <DeviceCard
                  v-for="device in filteredDevices"
                  :key="device.id"
                  :device="device"
                  :tags="tagStore.getTagsForDevice(device.id)"
                  @connect="connectDevice"
                  @settings="openSettings"
                  @edit-tags="id => openTagManager('single', id)"
                  @share="openShareModal"
                />
              </div>

              <!-- 离线设备区块（数据来自服务端离线记录） -->
              <div v-if="filteredOfflineDevices.length > 0" class="offline-section">
                <div class="offline-section-header">
                  <span class="offline-section-title">离线设备</span>
                  <span class="offline-section-count">{{ filteredOfflineDevices.length }}</span>
                </div>
                <div 
                  class="device-grid offline-grid" 
                  :style="{ gridTemplateColumns: gridColumnsStyle }"
                >
                  <DeviceCard
                    v-for="device in filteredOfflineDevices"
                    :key="device.id"
                    :device="device"
                    :tags="tagStore.getTagsForDevice(device.id)"
                    @connect="connectDevice"
                    @settings="openSettings"
                    @edit-tags="id => openTagManager('single', id)"
                    @share="openShareModal"
                  />
                </div>
              </div>
            </template>
          </template>
        </div>
      </main>
    </div>

    <SettingsModal 
      v-if="showSettingsModal" 
      :settings="localSettings" 
      :is-connected="false"
      :is-global="!selectedDeviceId"
      :is-custom="!!selectedDeviceId && hasCustomSettings(selectedDeviceId)"
      :locked-sections="policyLocked"
      :show-preview-tab="authStore.isAdmin"
      @close="closeSettings" 
      @save="saveSettings" 
      @reset="resetSettings"
    />

    <TagManagerModal
      v-if="showTagManager"
      :devices="tagManagerDevices"
      :mode="tagManagerMode"
      @close="closeTagManager"
    />

    <!-- 全局 ShareModal 弹窗 -->
    <ShareModal
      :visible="shareModalVisible"
      :deviceId="shareTargetDeviceId"
      @close="shareModalVisible = false"
    />

  </div>
</template>

<script setup>
import { computed, ref, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useDeviceStore } from '@/stores/devices'
import { useTagStore } from '@/stores/tags'
import DeviceCard from '@/components/DeviceCard.vue'
import DeviceListItem from '@/components/DeviceListItem.vue'
import SettingsModal from '@/components/SettingsModal.vue'
import TagManagerModal from '@/components/TagManagerModal.vue'
import ShareModal from '@/components/ShareModal.vue'

import { getDeviceSettings, saveDeviceSettings, hasCustomSettings, deleteDeviceSettings, applyPolicyToSettings, policyLockedSections } from '@/utils/settings'
import { useAuthStore } from '@/stores/auth'
import { useGroupControlStore } from '@/stores/groupControl'

const deviceStore = useDeviceStore()
const tagStore = useTagStore()
const groupControlStore = useGroupControlStore()

const shareModalVisible = ref(false)
const shareTargetDeviceId = ref('')

function openShareModal(deviceId) {
  shareTargetDeviceId.value = deviceId
  shareModalVisible.value = true
}
const cardSize = computed(() => deviceStore.cardSize)
const searchQuery = computed({
  get: () => deviceStore.searchQuery,
  set: (v) => { deviceStore.searchQuery = v }
})
const showTagDropdown = ref(false)
const viewMode = computed(() => deviceStore.viewMode)

function toggleViewMode() {
  deviceStore.toggleViewMode()
}

function openMultiDirectControl() {
  if (deviceStore.activeDeviceIds.length === 0) {
    const online = deviceStore.onlineDevices.slice(0, 2)
    if (online.length > 0) {
      online.forEach(d => deviceStore.openDevice(d.id))
    }
  }
}

// 移动端检测（写法与 App.vue 的 isMobile 保持一致）
const isMobile = ref(window.innerWidth <= 1024)
const updateMobileMedia = () => {
  isMobile.value = window.innerWidth <= 1024
}

// 移动端宫格列数：可选 2/3/4，默认 4，持久化到 localStorage
const savedMobileCols = parseInt(localStorage.getItem('cloudphone_mobile_cols'), 10)
const mobileCols = ref([2, 3, 4].includes(savedMobileCols) ? savedMobileCols : 4)
watch(mobileCols, (newVal) => {
  localStorage.setItem('cloudphone_mobile_cols', newVal.toString())
})

// 排序方式：default=按 id 字典序（现状），recent=最近活跃（lastSeen）优先
const savedSortBy = localStorage.getItem('cloudphone_sort_by')
const sortBy = ref(savedSortBy === 'recent' ? 'recent' : 'default')
watch(sortBy, (newVal) => {
  localStorage.setItem('cloudphone_sort_by', newVal)
})

// 网格列布局：移动端按 mobileCols 固定列数，桌面端按 cardSize 自适应（原逻辑）
const gridColumnsStyle = computed(() => {
  if (isMobile.value) {
    return `repeat(${mobileCols.value}, minmax(0, 1fr))`
  }
  return `repeat(auto-fill, minmax(${cardSize.value}px, 1fr))`
})

// 移动端页头交互状态：展开的下拉（'' = 全部收起）与搜索展开态
const mobileOpenMenu = ref('') // '' | 'batch' | 'tag' | 'sort' | 'cols'
const showMobileSearch = ref(false)
const mobileSearchInput = ref(null)

function toggleMobileMenu(name) {
  mobileOpenMenu.value = mobileOpenMenu.value === name ? '' : name
}

function closeMobileMenus() {
  mobileOpenMenu.value = ''
}

function toggleMobileSearch() {
  showMobileSearch.value = !showMobileSearch.value
  if (showMobileSearch.value) {
    nextTick(() => mobileSearchInput.value?.focus())
  }
}

// 失焦收起（有搜索内容时保留展开态）
function onMobileSearchBlur() {
  if (!searchQuery.value.trim()) {
    showMobileSearch.value = false
  }
}

function refreshDevices() {
  deviceStore.fetchDevices()
}

// 进入/退出群控（不指定主控机，进入后直接在卡片上勾选从机）
function toggleMobileGroupControl() {
  groupControlStore.toggleGroupControl()
}

function setSortBy(val) {
  sortBy.value = val
  closeMobileMenus()
}

function setMobileCols(n) {
  mobileCols.value = n
  closeMobileMenus()
}

// lastSeen 时间戳（无值或非法值视为 0，排序时排最后）
function lastSeenTime(device) {
  const t = device.lastSeen ? new Date(device.lastSeen).getTime() : 0
  return Number.isNaN(t) ? 0 : t
}

function selectAllOnline() {
  groupControlStore.selectAllOnline(deviceStore.devices)
}

function clearSlaves() {
  groupControlStore.clearSlaves()
}

function selectByTag(tagId) {
  groupControlStore.selectByTag(tagId, deviceStore.devices, tagStore)
  showTagDropdown.value = false
}

function toggleGlobalInteractive() {
  if (!deviceStore.globalInteractiveMode) {
    deviceStore.globalPreviewMode = true
    deviceStore.globalInteractiveMode = true
  } else {
    deviceStore.globalInteractiveMode = false
  }
}

// 点击页面空白处收起所有下拉（群控标签勾选 + 移动端页头下拉）
function closeTagDropdownMenu() {
  showTagDropdown.value = false
  closeMobileMenus()
}

onMounted(() => {
  window.addEventListener('click', closeTagDropdownMenu)
  window.addEventListener('resize', updateMobileMedia)
})

onUnmounted(() => {
  window.removeEventListener('click', closeTagDropdownMenu)
  window.removeEventListener('resize', updateMobileMedia)
  clearInterval(accountExpiryTimer)
})

watch(() => deviceStore.globalPreviewMode, (newVal) => {
  if (!newVal) {
    deviceStore.globalInteractiveMode = false
  }
})

watch(cardSize, (newVal) => {
  localStorage.setItem('cloudphone_card_size', newVal.toString())
})

let refreshInterval = null
const showSettingsModal = ref(false)
const selectedDeviceId = ref('')
const showTagManager = ref(false)
const tagManagerDevices = ref([])
const tagManagerMode = ref('full')

// 用户级设置管控：管理员配置的锁定项（码率/帧率/分辨率/音频）在 UI 置灰，服务端同步强制
const authStore = useAuthStore()
const policyLocked = computed(() => policyLockedSections(authStore.userPolicy))

// 移动端页头：账号剩余时间（/api/me 下发的 expires_at；零值时间=永久则不显示）
const accountNowTick = ref(Date.now())
let accountExpiryTimer = setInterval(() => { accountNowTick.value = Date.now() }, 1000)

const accountExpiryTime = computed(() => {
  const p = authStore.userPolicy
  if (!p || !p.expires_at) return null
  const t = new Date(p.expires_at)
  if (Number.isNaN(t.getTime()) || t.getFullYear() <= 1) return null
  return t
})
const accountExpired = computed(() => !!accountExpiryTime.value && accountExpiryTime.value.getTime() <= accountNowTick.value)
const accountExpiryChip = computed(() => {
  const t = accountExpiryTime.value
  if (!t) return ''
  const ms = t.getTime() - accountNowTick.value
  if (ms <= 0) return '已到期'
  const d = Math.floor(ms / 86400000)
  const h = Math.floor((ms % 86400000) / 3600000).toString().padStart(2, '0')
  const m = Math.floor((ms % 3600000) / 60000).toString().padStart(2, '0')
  const s = Math.floor((ms % 60000) / 1000).toString().padStart(2, '0')
  return d > 0 ? `剩 ${d} 天` : `剩 ${h}:${m}:${s}`
})

const localSettings = ref(applyPolicyToSettings(getDeviceSettings(''), authStore.userPolicy))
if (!authStore.userPolicy && authStore.token) {
  authStore.fetchMe().then(() => {
    localSettings.value = applyPolicyToSettings(localSettings.value, authStore.userPolicy)
  })
}

const filteredDevices = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()

  const result = deviceStore.devices.filter(device => {
    const deviceTags = tagStore.getTagsForDevice(device.id)
    const matchesTag = tagStore.selectedTagIds.length === 0 || 
      tagStore.selectedTagIds.every(id => deviceTags.some(tag => tag.id === id))
    if (!matchesTag) return false

    if (!query) return true

    const searchable = [
      device.id,
      device.info?.model,
      ...deviceTags.map(tag => tag.name)
    ].filter(Boolean).join(' ').toLowerCase()

    return searchable.includes(query)
  })

  // 排序：recent 按 lastSeen 最近优先（无 lastSeen 排最后）；default 保持原有顺序
  if (sortBy.value === 'recent') {
    return [...result].sort((a, b) => lastSeenTime(b) - lastSeenTime(a))
  }
  return result
})

// 离线设备（服务端离线记录），与在线列表使用相同的搜索/标签筛选
const filteredOfflineDevices = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()

  return deviceStore.offlineDevices.filter(device => {
    const deviceTags = tagStore.getTagsForDevice(device.id)
    const matchesTag = tagStore.selectedTagIds.length === 0 ||
      tagStore.selectedTagIds.every(id => deviceTags.some(tag => tag.id === id))
    if (!matchesTag) return false

    if (!query) return true

    const searchable = [
      device.id,
      device.info?.model,
      ...deviceTags.map(tag => tag.name)
    ].filter(Boolean).join(' ').toLowerCase()

    return searchable.includes(query)
  })
})

// 视图判断（兼容 table 与 list 模式名）
const isTableView = computed(() => ['table', 'list'].includes(deviceStore.viewMode))

const tableSortField = ref('id') // 'id' | 'status' | 'cpu' | 'lastSeen'
const tableSortAsc = ref(true)

function handleTableSort(field) {
  if (tableSortField.value === field) {
    tableSortAsc.value = !tableSortAsc.value
  } else {
    tableSortField.value = field
    tableSortAsc.value = field === 'id'
  }
}

function getSortIcon(field) {
  if (tableSortField.value !== field) return '↕'
  return tableSortAsc.value ? '▲' : '▼'
}

const isAllSelected = computed(() => {
  const online = filteredDevices.value.filter(d => d.status === 'online')
  return online.length > 0 && online.every(d => groupControlStore.selectedSlaveIds.includes(d.id))
})

function toggleSelectAll() {
  if (isAllSelected.value) {
    groupControlStore.clearSlaves()
  } else {
    groupControlStore.selectAllOnline(filteredDevices.value)
  }
}

const sortedDevices = computed(() => {
  const list = [...filteredDevices.value]
  return list.sort((a, b) => {
    let res = 0
    if (tableSortField.value === 'id') {
      res = a.id.localeCompare(b.id)
    } else if (tableSortField.value === 'status') {
      const aVal = a.status === 'online' ? 1 : 0
      const bVal = b.status === 'online' ? 1 : 0
      res = bVal - aVal
    } else if (tableSortField.value === 'cpu') {
      const aVal = a.metrics?.cpu || 0
      const bVal = b.metrics?.cpu || 0
      res = aVal - bVal
    } else if (tableSortField.value === 'lastSeen') {
      res = lastSeenTime(a) - lastSeenTime(b)
    }
    return tableSortAsc.value ? res : -res
  })
})

const sortedOfflineDevices = computed(() => {
  const list = [...filteredOfflineDevices.value]
  return list.sort((a, b) => {
    let res = 0
    if (tableSortField.value === 'id') {
      res = a.id.localeCompare(b.id)
    } else {
      res = lastSeenTime(a) - lastSeenTime(b)
    }
    return tableSortAsc.value ? res : -res
  })
})

// 当前视图是否无可展示设备（离线筛选模式下只看离线列表）
const noVisibleDevices = computed(() => {
  if (deviceStore.showOfflineOnly) {
    return filteredOfflineDevices.value.length === 0
  }
  return filteredDevices.value.length === 0 && filteredOfflineDevices.value.length === 0
})

function selectAllTags() {
  tagStore.clearSelectedTags()
  deviceStore.showOfflineOnly = false
}

function toggleOfflineView() {
  deviceStore.showOfflineOnly = !deviceStore.showOfflineOnly
  if (deviceStore.showOfflineOnly) {
    // 离线筛选与标签筛选互斥
    tagStore.clearSelectedTags()
  }
}

// 离线列表清空时自动退出离线筛选视图
watch(() => deviceStore.offlineDevices.length, len => {
  if (len === 0 && deviceStore.showOfflineOnly) {
    deviceStore.showOfflineOnly = false
  }
})

function openGlobalSettings() {
  selectedDeviceId.value = ''
  localSettings.value = applyPolicyToSettings(getDeviceSettings(''), authStore.userPolicy)
  showSettingsModal.value = true
}

function goToDeploy() {
  window.dispatchEvent(new CustomEvent('cloudphone-navigate', { detail: '/deploy' }))
}

function openSettings(deviceId) {
  selectedDeviceId.value = deviceId
  localSettings.value = applyPolicyToSettings(getDeviceSettings(deviceId), authStore.userPolicy)
  showSettingsModal.value = true
}

function closeSettings() {
  showSettingsModal.value = false
  selectedDeviceId.value = ''
}

async function saveSettings(newSettings) {
  // 弹窗关闭会清空选中项；异步保存完成后仍连接本次操作的设备。
  const deviceId = selectedDeviceId.value
  try {
    await saveDeviceSettings(deviceId, newSettings)
  } catch (err) {
    alert(err.message)
    return
  }
  localSettings.value = newSettings
  
  if (deviceId) {
    connectDevice(deviceId)
  }
  closeSettings()
}

async function resetSettings() {
  if (selectedDeviceId.value) {
    try {
      await deleteDeviceSettings(selectedDeviceId.value)
    } catch (err) {
      alert(err.message)
      return
    }
    closeSettings()
  }
}

function openTagManager(type, deviceId = '') {
  if (!authStore.isAdmin) return
  if (type === 'full') {
    tagManagerMode.value = 'full'
    tagManagerDevices.value = deviceStore.devices
  } else if (type === 'single' && deviceId) {
    tagManagerMode.value = 'assign'
    tagManagerDevices.value = deviceStore.devices.filter(d => d.id === deviceId)
  } else if (type === 'batch') {
    tagManagerMode.value = 'assign'
    const selectedIds = groupControlStore.selectedSlaveIds
    tagManagerDevices.value = deviceStore.devices.filter(d => selectedIds.includes(d.id))
  }
  showTagManager.value = true
}

function closeTagManager() {
  showTagManager.value = false
  tagManagerDevices.value = []
  tagManagerMode.value = 'full'
}

function tagFilterStyle(tag) {
  const active = tagStore.selectedTagIds.includes(tag.id)
  return {
    color: active ? '#fff' : 'var(--text-primary)',
    borderColor: `${tag.color}80`,
    background: active ? `${tag.color}35` : 'transparent'
  }
}

function getTagDeviceCount(tagId) {
  return deviceStore.devices.filter(device => tagStore.getTagIdsForDevice(device.id).includes(tagId)).length
}

function toggleSelectedTag(tagId) {
  tagStore.toggleSelectedTag(tagId)
  // 选择标签时退出离线筛选视图
  deviceStore.showOfflineOnly = false
}

function handleOpenGlobalSettingsEvent() {
  openGlobalSettings()
}

function handleOpenTagManagerEvent(e) {
  openTagManager(e?.detail?.mode || 'full')
}

onMounted(async () => {
  deviceStore.fetchDevices()
  refreshInterval = setInterval(() => {
    deviceStore.fetchDevices()
  }, 10000)
  window.addEventListener('cloudphone-open-tag-manager', handleOpenTagManagerEvent)
  window.addEventListener('open-tag-manager', handleOpenTagManagerEvent)
  window.addEventListener('open-global-settings', handleOpenGlobalSettingsEvent)
})

onUnmounted(() => {
  if (refreshInterval) clearInterval(refreshInterval)
  window.removeEventListener('cloudphone-open-tag-manager', handleOpenTagManagerEvent)
  window.removeEventListener('open-tag-manager', handleOpenTagManagerEvent)
  window.removeEventListener('open-global-settings', handleOpenGlobalSettingsEvent)
})

function connectDevice(deviceId) {
  // 默认卡片或列表点击均以屏幕连接为主，若未显式指定模式则确保为 display
  if (!deviceStore.getDeviceMode(deviceId)) {
    deviceStore.setDeviceMode(deviceId, 'display')
  }
  deviceStore.setActiveDevice(deviceId)
}
</script>

<style scoped>
.device-list-page {
  padding: 16px 20px;
  min-height: 100%;
}

.deploy-btn.secondary {
  background: rgba(255, 255, 255, 0.035);
  color: #d0d7de;
}

.deploy-btn.primary {
  color: #fff;
  background: rgba(88, 166, 255, 0.18);
  border-color: rgba(88, 166, 255, 0.35);
}

.mobile-tag-action {
  display: none;
}

.deploy-btn:hover {
  background: rgba(255, 255, 255, 0.06);
  border-color: rgba(255, 255, 255, 0.16);
}

.deploy-btn.primary:hover {
  background: rgba(88, 166, 255, 0.26);
  border-color: rgba(88, 166, 255, 0.5);
}

.size-control {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 36px;
  padding: 0 12px;
  background: rgba(255, 255, 255, 0.035);
  border: 1px solid var(--border);
  border-radius: 7px;
}

.preview-switches {
  display: contents;
}

.preview-mode-switch {
  display: flex;
  align-items: center;
  height: 36px;
  padding: 0 12px;
  background: rgba(255, 255, 255, 0.035);
  border: 1px solid var(--border);
  border-radius: 7px;
}

.switch-label {
  display: flex;
  align-items: center;
  gap: 8px;
  cursor: pointer;
  user-select: none;
}

.switch-label.disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.switch-label.disabled .switch-checkbox {
  cursor: not-allowed;
}

.switch-checkbox {
  cursor: pointer;
  accent-color: var(--accent);
}

.switch-text {
  font-size: 13px;
  color: var(--text-secondary);
}

.size-control .label {
  font-size: 13px;
  color: var(--text-secondary);
}

.size-slider {
  width: 96px;
  height: 4px;
  -webkit-appearance: none;
  background: var(--border);
  border-radius: 2px;
  outline: none;
}

.size-slider::-webkit-slider-thumb {
  -webkit-appearance: none;
  width: 14px;
  height: 14px;
  background: var(--accent);
  border-radius: 50%;
  cursor: pointer;
  transition: transform 0.1s;
}

.size-slider::-webkit-slider-thumb:hover {
  transform: scale(1.2);
}

.size-value {
  font-size: 12px;
  color: var(--text-secondary);
  min-width: 40px;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.content-layout {
  display: block;
}

.mobile-tag-bar {
  display: none;
}

.tag-filter {
  width: 100%;
  min-width: 0;
  height: 34px;
  display: grid;
  grid-template-columns: 10px minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  padding: 0 8px;
  border: 1px solid transparent;
  border-radius: 6px;
  color: var(--text-primary);
  background: transparent;
  text-align: left;
  font-size: 12px;
}

.tag-filter:hover {
  background: rgba(255, 255, 255, 0.06);
}

.tag-filter.active {
  border-color: var(--accent);
  background: rgba(233, 69, 96, 0.16);
}

.tag-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.tag-dot.all {
  background: var(--accent);
}

.tag-dot.offline {
  background: #8b949e;
}

.tag-name {
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.tag-count {
  min-width: 22px;
  padding: 1px 6px;
  border-radius: 999px;
  color: var(--text-secondary);
  background: rgba(255, 255, 255, 0.08);
  font-size: 11px;
  text-align: center;
}

.btn-refresh-icon {
  background: none;
  border: none;
  cursor: pointer;
  font-size: 18px;
  padding: 4px;
  border-radius: 4px;
  transition: background 0.2s;
}

.btn-refresh-icon:hover {
  background: rgba(255, 255, 255, 0.05);
}

.grid-container {
  min-width: 0;
  width: 100%;
}

/* 群控模式快捷工具条 */
.group-control-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  background: linear-gradient(90deg, rgba(255, 159, 67, 0.12) 0%, rgba(26, 115, 232, 0.08) 100%);
  border: 1px solid rgba(255, 159, 67, 0.35);
  border-radius: 8px;
  padding: 8px 14px;
  margin-bottom: 12px;
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
  position: relative;
  z-index: 50;
}

.gc-bar-left {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.gc-brand-badge {
  font-size: 12px;
  font-weight: 700;
  color: #ff9f43;
  display: flex;
  align-items: center;
  gap: 4px;
}

.gc-btn {
  background: rgba(255, 255, 255, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.15);
  color: var(--text-primary, #f1f5f9);
  font-size: 12px;
  font-weight: 500;
  padding: 4px 10px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.gc-btn:hover {
  background: rgba(255, 255, 255, 0.18);
  border-color: rgba(255, 255, 255, 0.3);
}

.gc-btn.gc-interactive-btn.active {
  background: rgba(56, 189, 248, 0.2);
  border-color: rgba(56, 189, 248, 0.5);
  color: #38bdf8;
  font-weight: 600;
}

.gc-tag-dropdown-wrap {
  position: relative;
  z-index: 60;
}

.gc-tag-dropdown-menu {
  position: absolute;
  top: 100%;
  left: 0;
  margin-top: 6px;
  background: #161b22;
  border: 1px solid rgba(255, 255, 255, 0.18);
  border-radius: 8px;
  box-shadow: 0 10px 30px rgba(0, 0, 0, 0.8);
  z-index: 1000;
  min-width: 150px;
  padding: 6px 0;
  max-height: 240px;
  overflow-y: auto;
}

.gc-tag-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  cursor: pointer;
  transition: background 0.15s ease;
  font-size: 12px;
  color: #f1f5f9;
}

.gc-tag-item:hover {
  background: rgba(255, 255, 255, 0.1);
}

.gc-tag-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.gc-tag-name {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.gc-tag-empty {
  padding: 8px 12px;
  color: #94a3b8;
  font-size: 12px;
  text-align: center;
}

.gc-count-badge {
  font-size: 11px;
  color: #38bdf8;
  background: rgba(56, 189, 248, 0.12);
  border: 1px solid rgba(56, 189, 248, 0.25);
  padding: 3px 8px;
  border-radius: 999px;
  font-weight: 600;
}

.gc-tag-action-btn {
  background: rgba(56, 189, 248, 0.15);
  border-color: rgba(56, 189, 248, 0.4);
  color: #38bdf8;
}

.gc-exit-btn {
  background: rgba(248, 81, 73, 0.12);
  border: 1px solid rgba(248, 81, 73, 0.3);
  color: #f85149;
  font-size: 12px;
  font-weight: 600;
  padding: 4px 10px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
}

.gc-exit-btn:hover {
  background: rgba(248, 81, 73, 0.25);
  border-color: rgba(248, 81, 73, 0.5);
}

.device-grid {
  display: grid;
  gap: 16px;
  grid-auto-flow: dense;
}

/* 高密运维数据表格 */
.device-table-container {
  --device-table-columns: 36px 46px minmax(120px, 1.5fr) 82px minmax(80px, 1fr) 130px minmax(70px, 1fr) 145px;
  background: var(--bg-secondary, #161b22);
  border: 1px solid var(--border, rgba(255, 255, 255, 0.1));
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.25);
  width: 100%;
}

.device-table-header {
  display: grid;
  grid-template-columns: var(--device-table-columns);
  align-items: center;
  padding: 8px 12px;
  background: rgba(13, 17, 23, 0.85);
  border-bottom: 1px solid var(--border, rgba(255, 255, 255, 0.12));
  font-size: 11px;
  font-weight: 700;
  color: var(--text-secondary, #94a3b8);
  letter-spacing: 0.04em;
  user-select: none;
}

.th {
  display: flex;
  align-items: center;
  padding: 0 6px;
  box-sizing: border-box;
  overflow: hidden;
  min-width: 0;
}

.th.col-actions { justify-content: flex-end; }

@media (max-width: 1200px) {
  .device-table-container { --device-table-columns: 36px 46px minmax(120px, 1.5fr) 82px minmax(80px, 1fr) minmax(70px, 1fr) 145px; }
  .th.col-metrics { display: none; }
}

@media (max-width: 1024px) {
  .device-table-container { --device-table-columns: 36px 46px minmax(100px, 1.5fr) 82px minmax(70px, 1fr) 145px; }
  .th.col-tags { display: none; }
}

@media (max-width: 640px) {
  .device-table-header { display: none; }
  .device-table-container { border-radius: 8px; }
}

.th.sortable {
  cursor: pointer;
  transition: color 0.15s;
}

.th.sortable:hover {
  color: #f1f5f9;
}

.sort-icon {
  font-size: 9px;
  margin-left: 4px;
  opacity: 0.7;
}

.header-checkbox {
  width: 15px;
  height: 15px;
  cursor: pointer;
  accent-color: var(--accent, #388bfd);
}

.table-offline-divider {
  padding: 8px 16px;
  background: rgba(15, 23, 42, 0.6);
  border-top: 1px dashed rgba(255, 255, 255, 0.1);
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  font-size: 11px;
  font-weight: 700;
  color: var(--text-secondary, #94a3b8);
  display: flex;
  align-items: center;
  gap: 6px;
}

/* 列表视图向后兼容 */
.device-list-view {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.offline-section {
  margin-top: 28px;
  padding-top: 16px;
  border-top: 1px dashed var(--border);
}

.offline-section-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 16px;
}

.offline-section-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-secondary, #94a3b8);
}

.offline-section-count {
  font-size: 12px;
  padding: 1px 8px;
  border-radius: 10px;
  background: rgba(148, 163, 184, 0.15);
  color: var(--text-secondary, #94a3b8);
}

.offline-grid :deep(.device-card) {
  filter: grayscale(0.55);
  opacity: 0.72;
  transition: filter 0.2s ease, opacity 0.2s ease;
}

.offline-grid :deep(.device-card:hover) {
  filter: grayscale(0.2);
  opacity: 0.95;
}

.state-view {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 100px 0;
  color: var(--text-secondary);
  text-align: center;
}

.spinner {
  width: 32px;
  height: 32px;
  border: 2px solid var(--border);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
  margin-bottom: 16px;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.empty-icon {
  font-size: 48px;
  margin-bottom: 16px;
  opacity: 0.5;
}

.state-view h3 {
  margin: 0 0 8px 0;
  color: var(--text-primary);
}

/* 移动端适配 */
@media (max-width: 1024px) {
  .device-list-page {
    padding: 8px 10px;
    height: 100%;
    display: flex;
    flex-direction: column;
  }

  .page-header {
    margin-bottom: 8px;
    padding-bottom: 8px;
  }

  /* 移动端紧凑页头：两行高密度控件 */
  .mobile-header {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .mh-row {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  /* 宫格等靠右下拉的面板右对齐，防止溢出屏幕右缘 */
  .mh-dropdown.drop-right .mh-panel {
    left: auto;
    right: 0;
  }

  /* 账号剩余时间胶囊（移动端页头） */
  .mh-expiry-chip {
    flex: 0 0 auto;
    font-size: 10px;
    font-weight: 600;
    color: #d29922;
    border: 1px solid rgba(210, 153, 34, 0.4);
    border-radius: 999px;
    padding: 3px 7px;
    white-space: nowrap;
  }

  .mh-expiry-chip.expired {
    color: #f85149;
    border-color: rgba(248, 81, 73, 0.5);
  }

  .mh-actions {
    display: flex;
    align-items: center;
    gap: 4px;
    margin-left: auto;
  }

  /* 行 1 右侧图标按钮 */
  .mh-icon-btn {
    width: 30px;
    height: 30px;
    flex: 0 0 auto;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: rgba(255, 255, 255, 0.035);
    color: var(--text-primary);
    font-size: 14px;
    cursor: pointer;
  }

  .mh-icon-btn svg {
    width: 15px;
    height: 15px;
  }

  .mh-icon-btn.active,
  .mh-icon-btn:hover {
    border-color: var(--accent);
    color: var(--accent);
  }

  /* 行内紧凑下拉触发按钮（单行排布，尺寸压到最小可用） */
  .mh-dropdown {
    position: relative;
  }

  .mh-filter-btn {
    height: 28px;
    padding: 0 8px;
    border: 1px solid var(--border);
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.035);
    color: var(--text-primary);
    font-size: 11px;
    cursor: pointer;
    white-space: nowrap;
  }

  .mh-filter-btn.active {
    border-color: var(--accent);
    color: var(--accent);
  }

  /* 下拉面板：宽度用 min() 限制，避免小屏溢出（风格参考群控"按标签勾选"下拉） */
  .mh-panel {
    position: absolute;
    top: 100%;
    left: 0;
    margin-top: 6px;
    min-width: 140px;
    max-width: min(72vw, 240px);
    max-height: 60vh;
    overflow-y: auto;
    background: #161b22;
    border: 1px solid rgba(255, 255, 255, 0.15);
    border-radius: 10px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
    z-index: 200;
    padding: 6px;
  }

  .mh-panel-item {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 8px 10px;
    border: none;
    border-radius: 6px;
    background: transparent;
    color: var(--text-primary);
    font-size: 12px;
    text-align: left;
    cursor: pointer;
    white-space: nowrap;
  }

  .mh-panel-item:hover {
    background: rgba(255, 255, 255, 0.06);
  }

  .mh-panel-item.active {
    color: var(--accent);
    background: rgba(88, 166, 255, 0.12);
  }

  .mh-item-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .mh-item-count {
    margin-left: auto;
    min-width: 20px;
    padding: 1px 6px;
    border-radius: 999px;
    background: rgba(255, 255, 255, 0.08);
    color: var(--text-secondary);
    font-size: 11px;
    text-align: center;
    flex: 0 0 auto;
  }

  .mh-panel-static {
    padding: 4px 10px;
    font-size: 11px;
    color: var(--text-secondary);
  }

  .mh-panel-divider {
    height: 1px;
    margin: 4px 6px;
    background: var(--border);
  }

  /* 批量弹层内的预览开关行 */
  .mh-switch {
    padding: 8px 10px;
  }

  /* 搜索展开态：整行输入框 */
  .mh-search-row .search-box {
    width: 100%;
    height: 34px;
  }

  .content-layout {
    min-height: 0;
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  /* 移动端主区域改为垂直滚动，设备网格/列表均自然向下滚动浏览 */
  .grid-container {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
  }

  /* 列数由内联样式按 mobileCols 输出（2/3/4 列），此处只控制间距 */
  .device-grid {
    gap: 8px;
    padding: 2px 2px 12px;
  }

  .device-grid > * {
    min-width: 0;
    height: auto;
    aspect-ratio: 3 / 4;
  }

  .device-list-view {
    gap: 8px;
    padding-bottom: 12px;
  }
}

/* 群控开关样式 */
/* 群控快捷操作面板 */
.group-quick-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 8px;
  padding: 4px 10px;
  margin-right: 12px;
}

.action-btn-mini {
  background: rgba(255, 255, 255, 0.08);
  border: 1px solid rgba(255, 255, 255, 0.1);
  color: var(--text-primary);
  font-size: 12px;
  padding: 4px 8px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
}

.action-btn-mini:hover {
  background: rgba(255, 255, 255, 0.15);
  border-color: rgba(255, 255, 255, 0.2);
}

.action-btn-mini.dropdown-trigger {
  position: relative;
}

/* 标签下拉菜单 */
.tag-select-dropdown {
  position: relative;
}

.tag-dropdown-menu {
  position: absolute;
  top: 100%;
  left: 0;
  margin-top: 6px;
  background: #161b22;
  border: 1px solid rgba(255, 255, 255, 0.15);
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
  z-index: 100;
  min-width: 130px;
  padding: 6px 0;
  max-height: 200px;
  overflow-y: auto;
}

.tag-dropdown-menu::-webkit-scrollbar {
  width: 4px;
}

.tag-dropdown-menu::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.2);
  border-radius: 2px;
}

.tag-dropdown-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  cursor: pointer;
  transition: background 0.2s ease;
  font-size: 12px;
  color: var(--text-primary);
}

.tag-dropdown-item:hover {
  background: rgba(255, 255, 255, 0.06);
}

.tag-color-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.tag-dropdown-empty {
  padding: 8px 12px;
  color: var(--text-secondary);
  font-size: 12px;
  text-align: center;
}

.selected-count-badge {
  font-size: 11px;
  color: var(--accent);
  background: rgba(26, 115, 232, 0.12);
  padding: 2px 6px;
  border-radius: 4px;
  font-weight: 500;
}

.group-mode-badge {
  font-size: 11px;
  color: #ff9f43;
  background: rgba(255, 159, 67, 0.12);
  border: 1px solid rgba(255, 159, 67, 0.25);
  padding: 2px 8px;
  border-radius: 4px;
  font-weight: 600;
  white-space: nowrap;
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 简单淡入动画 */
.animate-fade-in {
  animation: fadeIn 0.2s cubic-bezier(0.16, 1, 0.3, 1);
}

@keyframes fadeIn {
  from { opacity: 0; transform: translateY(-4px); }
  to { opacity: 1; transform: translateY(0); }
}

.deploy-start-btn {
  padding: 10px 18px;
  color: #fff;
  background: #238636;
  border: 1px solid #2ea043;
  border-radius: 6px;
  cursor: pointer;
}
</style>
