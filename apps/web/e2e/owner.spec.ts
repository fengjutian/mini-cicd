import { expect, test } from '@playwright/test'

test('Owner 初始化、导航和系统状态', async ({ page }) => {
  await page.goto('/')
  const setup = page.getByRole('heading', { name: '创建 Owner' })
  if (await setup.isVisible().catch(() => false)) {
    await page.getByLabel('用户名').fill('owner')
    await page.getByLabel('邮箱').fill('owner@example.com')
    await page.getByLabel('密码', { exact: true }).fill('Correct-Horse-Battery-42')
    await page.getByLabel('确认密码').fill('Correct-Horse-Battery-42')
    await page.getByRole('button', { name: '完成初始化' }).click()
  } else if (await page.getByRole('heading', { name: '欢迎回来' }).isVisible().catch(() => false)) {
    await page.getByLabel('邮箱').fill('owner@example.com')
    await page.getByLabel('密码').fill('Correct-Horse-Battery-42')
    await page.getByRole('button', { name: '登录' }).click()
  }
  await expect(page.getByRole('heading', { name: '部署控制台' })).toBeVisible()
  await page.getByRole('link', { name: '项目' }).click()
  await expect(page.getByRole('heading', { name: '项目' })).toBeVisible()
  await page.getByRole('link', { name: '系统设置' }).click()
  await expect(page.getByText('运行依赖检查')).toBeVisible()
  await expect(page.getByText('审计日志')).toBeVisible()
})
