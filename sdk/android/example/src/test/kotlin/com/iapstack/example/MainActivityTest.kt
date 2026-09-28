package com.iapstack.example

import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

@OptIn(ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [28])
class MainActivityTest {
  @Test fun failedStartupCanBeRetriedWithoutLosingLogin() {
    Dispatchers.setMain(UnconfinedTestDispatcher())
    val controller = Robolectric.buildActivity(MainActivity::class.java).setup()
    try {
      val views = descendants(controller.get().findViewById(android.R.id.content))
      val login = views.filterIsInstance<EditText>().single { it.hint.toString().startsWith("Host login token") }
      val connect = views.filterIsInstance<Button>().single { it.text == "Start customer session" }
      login.setText("temporary-test-login")
      // An empty product ID fails before any network or store request.
      repeat(2) {
        connect.performClick()
        assertTrue(connect.isEnabled)
        assertEquals("temporary-test-login", login.text.toString())
        assertFalse(login.isSaveEnabled)
        assertTrue(views.filterIsInstance<TextView>().any { it.text.startsWith("Request failed.") })
      }
    } finally {
      controller.pause().stop().destroy()
      Dispatchers.resetMain()
    }
  }

  private fun descendants(view: View): List<View> = listOf(view) +
    if (view is ViewGroup) (0 until view.childCount).flatMap { descendants(view.getChildAt(it)) } else emptyList()
}
